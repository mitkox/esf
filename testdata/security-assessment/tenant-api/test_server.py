"""Run in Cube: python3 -m unittest discover -s . -p 'test_*.py' -v."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from urllib.error import HTTPError
from urllib.request import HTTPRedirectHandler, Request, build_opener


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, new_url):
        return None


class TenantAPITest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.scratch = tempfile.TemporaryDirectory()
        cls.logs = Path(cls.scratch.name) / "access.jsonl"
        cls.token = "synthetic-app-token-for-tests"
        cls.redirect = "https://outside.invalid/escape"
        cls.proc = subprocess.Popen(
            [sys.executable, str(Path(__file__).with_name("server.py")),
             "--bind", "127.0.0.1", "--port", "0", "--log", str(cls.logs)],
            env={**os.environ, "TARGET_BEARER_TOKEN": cls.token,
                 "TARGET_RUN_ID": "qualification-test", "TARGET_INSTANCE_ID": "target-test",
                 "TARGET_REDIRECT_URL": cls.redirect},
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        try:
            ready = json.loads(cls.proc.stdout.readline())
            cls.url = f"http://127.0.0.1:{ready['port']}"
            if not ready["ready"]:
                raise RuntimeError("target did not become ready")
        except Exception:
            cls.proc.terminate()
            cls.proc.wait(timeout=5)
            cls.scratch.cleanup()
            raise

    @classmethod
    def tearDownClass(cls):
        cls.proc.terminate()
        cls.proc.wait(timeout=5)
        cls.proc.stdout.close()
        cls.proc.stderr.close()
        cls.scratch.cleanup()

    def request(self, path, *, token=None, correlation="fixture-test", user="a", auth=True):
        headers = {"X-Correlation-ID": correlation, "X-Synthetic-User": user}
        if auth:
            headers["Authorization"] = "Bearer " + (token or self.token)
        request = Request(self.url + path, headers=headers)
        try:
            response = build_opener(NoRedirect).open(request, timeout=3)
        except HTTPError as error:
            response = error
        with response:
            return response.status, dict(response.headers), json.load(response)

    def test_health_requires_valid_application_token(self):
        for auth, token in [(False, None), (True, "wrong-token")]:
            with self.subTest(auth=auth):
                status, _, body = self.request("/health", auth=auth, token=token)
                self.assertEqual(status, 401)
                self.assertNotIn("instance_id", body)

    def test_health_identifies_run_and_instance(self):
        status, headers, body = self.request("/health", correlation="health-identity")
        self.assertEqual(status, 200)
        self.assertEqual(body["run_id"], "qualification-test")
        self.assertEqual(body["instance_id"], "target-test")
        self.assertEqual(body["profile"], "synthetic-tenant-api/v1")
        self.assertTrue(body["log_complete"])
        self.assertEqual(headers["X-Correlation-ID"], "health-identity")

    def test_authorized_control_and_deliberately_seeded_cross_tenant_read(self):
        for resource, expected_owner in [("a", "a"), ("b", "b")]:
            with self.subTest(resource=resource):
                status, _, body = self.request("/api/resources/" + resource, user="a")
                self.assertEqual(status, 200)
                self.assertEqual(body["owner"], expected_owner)
                self.assertEqual(body["requested_by"], "a")

    def test_correlation_is_echoed_and_logged_without_credentials(self):
        correlation = "log-proof-35fbf334"
        status, headers, body = self.request("/api/resources/a", correlation=correlation)
        self.assertEqual(status, 200)
        self.assertEqual(headers["X-Correlation-ID"], correlation)
        self.assertEqual(body["correlation_id"], correlation)
        entries = [json.loads(line) for line in self.logs.read_text().splitlines()]
        entry = next(item for item in entries if item["correlation_id"] == correlation)
        self.assertEqual(entry["status"], 200)
        self.assertEqual(entry["path"], "/api/resources/a")
        self.assertEqual(entry["instance_id"], "target-test")
        self.assertNotIn(self.token, self.logs.read_text())

    def test_redirect_is_only_operator_configured_destination(self):
        status, headers, _ = self.request("/redirect")
        self.assertEqual(status, 302)
        self.assertEqual(headers["Location"], self.redirect)
        status, headers, _ = self.request("/redirect?url=http://untrusted.invalid/")
        self.assertEqual(status, 404)
        self.assertNotIn("Location", headers)

    def test_unknown_path_and_user_do_not_expose_data(self):
        self.assertEqual(self.request("/../../etc/passwd")[0], 404)
        self.assertEqual(self.request("/api/resources/a", user="unknown")[0], 403)

    def test_invalid_correlation_is_not_reflected(self):
        status, headers, body = self.request("/health", correlation="unsafe id")
        self.assertEqual(status, 400)
        self.assertEqual(headers["X-Correlation-ID"], "invalid")
        self.assertNotIn("unsafe id", json.dumps(body))

    def test_log_cap_is_visible_as_incomplete(self):
        capped_logs = Path(self.scratch.name) / "capped.jsonl"
        process = subprocess.Popen(
            [sys.executable, str(Path(__file__).with_name("server.py")),
             "--bind", "127.0.0.1", "--port", "0", "--log", str(capped_logs),
             "--max-log-bytes", "1"],
            env={**os.environ, "TARGET_BEARER_TOKEN": self.token,
                 "TARGET_RUN_ID": "capped-test", "TARGET_INSTANCE_ID": "capped-instance"},
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        try:
            ready = json.loads(process.stdout.readline())
            url = f"http://127.0.0.1:{ready['port']}/health"
            for _ in range(2):
                with build_opener(NoRedirect).open(
                    Request(url, headers={"Authorization": "Bearer " + self.token}), timeout=3
                ) as response:
                    body = json.load(response)
            self.assertFalse(body["log_complete"])
            self.assertLessEqual(capped_logs.stat().st_size, 1)
        finally:
            process.terminate()
            process.wait(timeout=5)
            process.stdout.close()
            process.stderr.close()


if __name__ == "__main__":
    unittest.main()
