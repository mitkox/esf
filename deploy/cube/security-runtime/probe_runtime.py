"""Qualify selected native MCP contracts inside a disposable Cube VM."""
import json
import os
from pathlib import Path
import selectors
import signal
import subprocess
import sys
import tempfile
import time

REQUIRED = ("oc_catalog", "oc_engagement_create", "oc_http_request", "oc_log_timeline",
            "oc_evidence_export", "oc_evidence_verify", "oc_audit_verify")


def main():
    workspace = Path(tempfile.mkdtemp(prefix="opencyber-runtime-qualification-"))
    env = dict(os.environ, OPENCYBER_WORKSPACE=str(workspace), OPENCYBER_MCP_MODE="compact",
               OPENCYBER_ENFORCE_SCOPE="1", OPENCYBER_AUDIT_REQUIRED="1", OPENCYBER_STRICT_CATALOG="1")
    process = subprocess.Popen([sys.executable, "-m", "opencyber.server"], stdin=subprocess.PIPE,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env,
                               start_new_session=True)
    selector = selectors.DefaultSelector()
    selector.register(process.stdout, selectors.EVENT_READ, "out")
    selector.register(process.stderr, selectors.EVENT_READ, "err")
    buffer = b""
    errors = b""
    sequence = 0

    def send(message):
        process.stdin.write(json.dumps(message).encode() + b"\n")
        process.stdin.flush()

    def request(method, params):
        nonlocal buffer, errors, sequence
        sequence += 1
        expected = sequence
        send({"jsonrpc": "2.0", "id": expected, "method": method, "params": params})
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            while b"\n" in buffer:
                line, buffer = buffer.split(b"\n", 1)
                result = json.loads(line)
                if result.get("id") == expected:
                    if "error" in result:
                        raise RuntimeError("MCP protocol failure")
                    return result["result"]
            for key, _ in selector.select(max(0, deadline-time.monotonic())):
                chunk = os.read(key.fileobj.fileno(), 65536)
                if not chunk:
                    raise RuntimeError("MCP process disconnected: " + errors.decode(errors="replace")[-1000:])
                if key.data == "err":
                    errors = (errors + chunk)[-4096:]
                else:
                    buffer += chunk
                    if len(buffer) > 4*1024*1024:
                        raise RuntimeError("MCP response exceeded qualification limit")
        raise TimeoutError("MCP request timeout")

    def call(name, arguments):
        result = request("tools/call", {"name": name, "arguments": arguments})
        if result.get("isError") or not isinstance(result.get("structuredContent"), dict):
            raise RuntimeError("missing structured tool result: " + name)
        data = result["structuredContent"]
        if data.get("ok") is False:
            raise RuntimeError(name + " failed: " + json.dumps(data.get("error")))
        return data

    try:
        server = request("initialize", {"protocolVersion":"2025-03-26", "capabilities":{},
                         "clientInfo":{"name":"esf-cube-runtime-qualification","version":"1"}})
        send({"jsonrpc":"2.0","method":"notifications/initialized"})
        tools = request("tools/list", {})["tools"]
        listed = {tool["name"]:tool for tool in tools}
        if any(name not in listed for name in REQUIRED):
            raise RuntimeError("required native tool missing")
        catalog = call("oc_catalog", {"expected_catalog_digest":"1c50da349bf4e7e162e34f3554840d7190926ddd559c7b0f23628ea91bb92837"})
        if (catalog.get("catalog_digest") != "1c50da349bf4e7e162e34f3554840d7190926ddd559c7b0f23628ea91bb92837"
                or catalog.get("recorded_catalog_digest") != "6ff0d628d1e93da78345b05ad63cdcd27adade55de29938ead97c4a7560b6660"
                or catalog.get("catalog_subset_matches_release") is not True):
            raise RuntimeError("catalog identity or native subset alignment mismatch")
        engagement = call("oc_engagement_create", {"name":"Cube runtime qualification", "targets":["https://synthetic.invalid"], "objective":"selected native tools and evidence only"})
        engagement_id = engagement["engagement_id"]
        root = workspace / "engagements" / engagement_id
        supplied = root / "evidence" / "supplied.jsonl"
        supplied.write_text('{"timestamp":"2026-10-27T09:00:00Z","correlation_id":"runtime-control","event":"synthetic-denial"}\n', encoding="utf-8")
        timeline = call("oc_log_timeline", {"engagement_id":engagement_id, "path":"evidence/supplied.jsonl", "format":"jsonl", "max_bytes":65536,"max_records":10})
        exported = call("oc_evidence_export", {"engagement_id":engagement_id,"paths":["evidence/supplied.jsonl"]})
        verified = call("oc_evidence_verify", {"engagement_id":engagement_id,"path":exported["path"],"expected_sha256":exported["sha256"]})
        audit = call("oc_audit_verify", {"engagement_id":engagement_id})
        if timeline.get("complete") is not True or timeline.get("coverage", {}).get("status") != "complete":
            raise RuntimeError("supplied-log coverage incomplete")
        if verified.get("valid") is not True or audit.get("valid") is not True:
            raise RuntimeError("selected evidence or audit verification incomplete")
        print(json.dumps({"server":server["serverInfo"], "required_tools":list(REQUIRED),
                          "catalog":catalog,"timeline":timeline,"export":exported,
                          "export_file":str(root/exported["path"]),"verified":verified,"audit":audit}, sort_keys=True))
    finally:
        process.stdin.close()
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid,signal.SIGTERM)
            process.wait(timeout=3)
        selector.close()


if __name__ == "__main__":
    main()
