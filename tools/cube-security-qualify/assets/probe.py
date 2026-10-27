"""Fixed endpoint qualification; execute only inside the owned assessor Cube."""
import http.client
import json
import socket
import ssl
import sys
import time
from urllib.parse import urlsplit


def request(url, config, *, endpoint=True, app=True, correlation="", host_header=None,
            method="GET", request_path=None):
    parsed = urlsplit(url)
    if parsed.scheme != "https" or parsed.username or parsed.password:
        raise ValueError("HTTPS endpoint without userinfo required")
    conn = http.client.HTTPSConnection(parsed.hostname, parsed.port, timeout=4,
        context=ssl.create_default_context(cafile=config.get("ca_file")))
    conn._create_connection = lambda address, timeout, *args: socket.create_connection(
        (config["proxy_ip"], int(config["proxy_port"])), timeout)
    headers = {"X-Correlation-ID": correlation}
    if endpoint:
        headers["X-ESF-Endpoint-Token"] = config["endpoint_token"]
    if app:
        headers["Authorization"] = "Bearer " + config["app_token"]
    if host_header is not None:
        headers["Host"] = host_header
    try:
        conn.request(method, request_path or parsed.path or "/", headers=headers)
        response = conn.getresponse()
        raw = response.read(8193)
        if len(raw) > 8192:
            raise ValueError("response truncated")
        return response.status, raw, response.getheader("X-Correlation-ID"), response.getheader("Location")
    finally:
        conn.close()


def network_control(config, protocol):
    payload = (config["owner"] + "-" + protocol + "-control").encode("ascii")
    port = int(config["proxy_port"]) + (1 if protocol == "tcp" else 0)
    kind = socket.SOCK_STREAM if protocol == "tcp" else socket.SOCK_DGRAM
    with socket.socket(socket.AF_INET, kind) as client:
        client.settimeout(3)
        connected = False
        try:
            client.connect((config["proxy_ip"], port))
            connected = protocol == "tcp"
            client.sendall(payload)
            return {"protocol": protocol, "correlation": payload.decode(), "reachable": client.recv(512) == payload, "connected": connected}
        except OSError as error:
            return {"protocol": protocol, "correlation": payload.decode(), "reachable": False, "connected": connected, "errno": error.errno}


def run(config):
    checks = []
    def add(name, status, detail):
        checks.append({"name": name, "status": status, "detail": detail})
    base = config["target_url"]
    cid = config["owner"] + "-positive"
    for attempt in range(5):
        try:
            code, raw, echoed, _ = request(base + "/health", config, correlation=cid)
            body = json.loads(raw)
            valid = code == 200 and body.get("run_id") == config["owner"] and body.get("instance_id") == config["target_id"] and echoed == cid
            add("positive-control", "pass" if valid else "fail", "TLS, target instance and request identity verified" if valid else "positive-control identity mismatch")
            break
        except (OSError, ValueError, http.client.HTTPException) as error:
            if attempt == 4:
                add("positive-control", "incomplete", "positive control unavailable (" + type(error).__name__ + "); denial cannot be inferred")
            else:
                time.sleep(0.2)
    if any(c["status"] != "pass" for c in checks):
        return checks
    def check_request(name, path="/health", expected=403, **kwargs):
        try:
            code, _, _, _ = request(base + path, config, correlation=config["owner"] + "-" + name, **kwargs)
            add(name, "pass" if code == expected else "fail", "HTTP " + str(code))
        except (OSError, ValueError, http.client.HTTPException):
            add(name, "incomplete", "request unavailable; earlier evidence retained")
    check_request("forbidden-target-port", host_header=urlsplit(config["forbidden_port_url"]).hostname)
    check_request("path-routing-escape", path="/sandbox/" + config["target_id"] + "/18081/health")
    check_request("encoded-path-escape", path="/%73andbox/other/18081/health")
    check_request("absolute-uri-escape", request_path=config["forbidden_port_url"] + "/health")
    check_request("method-escape", method="POST")
    check_request("endpoint-authorization", endpoint=False)
    check_request("application-authorization", expected=401, app=False)
    check_request("redirect-escape", path="/redirect")
    for name, ip, port in (("host-management-denial", config.get("management_ip"), 4000),
                           ("direct-preview-denial", config.get("management_ip"), 9443),
                           ("metadata-denial", "169.254.169.254", 80)):
        if not ip:
            add(name, "incomplete", "no declared diagnostic destination")
            continue
        try:
            with socket.create_connection((ip, port), timeout=2):
                add(name, "fail", "forbidden TCP destination reachable")
        except OSError:
            add(name, "pass", "direct TCP connection denied under deny-all except owned endpoint address")
    check_request("positive-control-after-denials", expected=200)
    return checks


if __name__ == "__main__":
    try:
        with open(sys.argv[1], encoding="utf-8") as source:
            config = json.load(source)
        if len(sys.argv) == 3 and sys.argv[2] in ("tcp", "udp"):
            print(json.dumps(network_control(config, sys.argv[2]), sort_keys=True))
            sys.exit(0)
        checks = run(config)
    except (OSError, ValueError, KeyError, http.client.HTTPException):
        checks = [{"name": "probe", "status": "incomplete", "detail": "transport/input error; no absence-of-findings pass"}]
    print(json.dumps({"checks": checks}, sort_keys=True))
    sys.exit(1 if any(c["status"] != "pass" for c in checks) else 0)
