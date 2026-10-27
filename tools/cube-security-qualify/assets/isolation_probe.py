"""Attempt fixed writes from the untrusted target; execute only in Cube."""
import json
import socket
import sys


def run(proxy_ip, endpoint_ip):
    checks = []
    for name, address, port in (
        ("target-assessor-preview-denial", proxy_ip, 9443),
        ("target-management-denial", proxy_ip, 4000),
        ("target-endpoint-denial", endpoint_ip, 9445),
    ):
        try:
            with socket.create_connection((address, port), timeout=2):
                checks.append({"name": name, "status": "fail", "detail": "target reached forbidden listener"})
        except OSError:
            checks.append({"name": name, "status": "pass", "detail": "target TCP denied; factory holds listener positive control"})
    # An identically named local file must not affect the assessor filesystem.
    with open("/tmp/isolation-sentinel", "w", encoding="ascii") as output:
        output.write("target-local-write")
    return checks


if __name__ == "__main__":
    checks = run(*sys.argv[1:])
    print(json.dumps({"checks": checks}, sort_keys=True))
    sys.exit(any(c["status"] != "pass" for c in checks))
