"""Deliberately vulnerable tenant API. Execute only in disposable Cube labs."""

import argparse
import hmac
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os
from pathlib import Path
import re
import time
from urllib.parse import urlsplit
import uuid


IDENTIFIER = re.compile(r"[A-Za-z0-9._-]{1,128}\Z")
PROFILE = "synthetic-tenant-api/v1"
ROUTES = {"/health", "/api/resources/a", "/api/resources/b", "/redirect"}


class Target(HTTPServer):
    def __init__(self, address, *, token, run_id, instance_id, redirect, log, max_log_bytes):
        self.token = token
        self.run_id = run_id
        self.instance_id = instance_id
        self.redirect = redirect
        self.log_file = log
        self.max_log_bytes = max_log_bytes
        self.log_bytes = 0
        self.log_complete = True
        super().__init__(address, Handler)

    def record(self, path, correlation, status):
        event = {
            "time_ns": time.time_ns(), "run_id": self.run_id,
            "instance_id": self.instance_id, "correlation_id": correlation,
            "path": path if path in ROUTES else "unknown", "status": status,
        }
        line = json.dumps(event, sort_keys=True, separators=(",", ":")) + "\n"
        size = len(line.encode("utf-8"))
        if not self.log_complete or self.log_bytes + size > self.max_log_bytes:
            self.log_complete = False
            return
        try:
            self.log_file.write(line)
            self.log_file.flush()
        except OSError:
            self.log_complete = False
            return
        self.log_bytes += size


class Handler(BaseHTTPRequestHandler):
    server_version = "SyntheticTenantAPI/1"
    sys_version = ""

    def do_GET(self):
        correlation = self.headers.get("X-Correlation-ID") or str(uuid.uuid4())
        if not IDENTIFIER.fullmatch(correlation):
            self.respond(400, {"error": "invalid correlation identity"}, "invalid")
            return

        supplied = self.headers.get("Authorization", "").encode("utf-8")
        expected = ("Bearer " + self.server.token).encode("utf-8")
        if not hmac.compare_digest(supplied, expected):
            self.respond(401, {"error": "application token required"}, correlation)
            return

        if self.path == "/health":
            self.respond(200, {
                "ready": True, "profile": PROFILE, "run_id": self.server.run_id,
                "instance_id": self.server.instance_id,
                "log_complete": self.server.log_complete,
            }, correlation)
        elif self.path in {"/api/resources/a", "/api/resources/b"}:
            user = self.headers.get("X-Synthetic-User", "")
            if user not in {"a", "b"}:
                self.respond(403, {"error": "synthetic user required"}, correlation)
                return
            owner = self.path.rsplit("/", 1)[-1]
            self.respond(200, {"id": owner, "owner": owner, "requested_by": user,
                               "value": "synthetic-resource-" + owner}, correlation)
        elif self.path == "/redirect" and self.server.redirect:
            self.respond(302, {"redirect": True}, correlation, location=self.server.redirect)
        else:
            self.respond(404, {"error": "unknown fixture route"}, correlation)

    def respond(self, status, data, correlation, *, location=None):
        data["correlation_id"] = correlation
        body = json.dumps(data, sort_keys=True).encode("utf-8")
        self.server.record(self.path, correlation, status)
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("X-Correlation-ID", correlation)
        self.send_header("Cache-Control", "no-store")
        if location is not None:
            self.send_header("Location", location)
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        # The bounded structured log is the only request log. BaseHTTPRequestHandler
        # would otherwise echo arbitrary paths into stderr.
        pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bind", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=8080)
    parser.add_argument("--log", type=Path, default=Path("/tmp/tenant-api-access.jsonl"))
    parser.add_argument("--max-log-bytes", type=int, default=64 * 1024)
    args = parser.parse_args()
    token = os.environ.get("TARGET_BEARER_TOKEN", "")
    run_id = os.environ.get("TARGET_RUN_ID", "")
    instance_id = os.environ.get("TARGET_INSTANCE_ID", "")
    redirect = os.environ.get("TARGET_REDIRECT_URL", "")
    if len(token) < 16 or len(token) > 1024 or not token.isascii() or any(c.isspace() for c in token):
        parser.error("TARGET_BEARER_TOKEN must contain 16 to 1024 non-whitespace ASCII characters")
    if not IDENTIFIER.fullmatch(run_id) or not IDENTIFIER.fullmatch(instance_id):
        parser.error("TARGET_RUN_ID and TARGET_INSTANCE_ID must be nonempty safe identifiers")
    if not 0 <= args.port <= 65535:
        parser.error("--port must be between 0 and 65535")
    if not 1 <= args.max_log_bytes <= 16 * 1024 * 1024:
        parser.error("--max-log-bytes must be between 1 and 16777216")
    if redirect:
        parsed = urlsplit(redirect)
        if (len(redirect) > 2048 or parsed.scheme not in {"http", "https"}
                or not parsed.hostname or parsed.username is not None or parsed.password is not None
                or any(c.isspace() for c in redirect)):
            parser.error("TARGET_REDIRECT_URL must be an HTTP(S) URL without credentials or whitespace")
    args.log.parent.mkdir(parents=True, exist_ok=True)
    try:
        descriptor = os.open(args.log, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    except OSError as error:
        parser.error(f"cannot create fresh fixture log: {error}")
    with os.fdopen(descriptor, "w", encoding="utf-8") as log:
        with Target((args.bind, args.port), token=token, run_id=run_id, instance_id=instance_id,
                    redirect=redirect, log=log, max_log_bytes=args.max_log_bytes) as server:
            print(json.dumps({"ready": True, "port": server.server_port, "run_id": run_id,
                              "instance_id": instance_id}), flush=True)
            server.serve_forever()


if __name__ == "__main__":
    main()
