"""Decision regressions; live transport is qualified separately in Cube."""
import importlib.util
import json
from pathlib import Path
import ssl
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("probe", Path(__file__).with_name("probe.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


class ProbeTests(unittest.TestCase):
    def setUp(self):
        self.config = {"target_url": "https://18080-target.cube.test",
                       "forbidden_port_url": "https://18081-target.cube.test",
                       "owner": "qualification-test", "target_id": "target"}

    def response(self, url, config, **kwargs):
        correlation = kwargs.get("correlation", "")
        if kwargs.get("endpoint") is False:
            return 403, b"denied", None, None
        if kwargs.get("app") is False:
            return 401, b"denied", correlation, None
        if url.endswith("/redirect"):
            return 403, b"redirect denied", correlation, None
        if "/sandbox/" in url or "/%73andbox/" in url or kwargs.get("request_path") or kwargs.get("method") == "POST":
            return 403, b"scope denied", None, None
        return 200, json.dumps({"run_id": config["owner"], "instance_id": config["target_id"]}).encode(), correlation, None

    def test_reachable_forbidden_port_is_no_go(self):
        with patch.object(probe, "request", side_effect=self.response):
            result = {c["name"]: c for c in probe.run(self.config)}
        self.assertEqual(result["positive-control"]["status"], "pass")
        self.assertEqual(result["forbidden-target-port"]["status"], "fail")
        self.assertEqual(result["redirect-escape"]["status"], "pass")
        self.assertEqual(result["endpoint-authorization"]["status"], "pass")

    def test_wrong_instance_cannot_be_a_positive_control(self):
        def wrong(url, config, **kwargs):
            return 200, b'{"run_id":"qualification-test","instance_id":"other"}', kwargs["correlation"], None
        with patch.object(probe, "request", side_effect=wrong):
            checks = probe.run(self.config)
        self.assertTrue(all(c["status"] == "fail" for c in checks))
        self.assertNotIn("forbidden-target-port", {c["name"] for c in checks})

    def test_tls_failure_is_incomplete_not_successful_denial(self):
        with patch.object(probe, "request", side_effect=ssl.SSLError("untrusted CA")), patch.object(probe.time, "sleep"):
            checks = probe.run(self.config)
        self.assertTrue(checks)
        self.assertTrue(all(c["status"] == "incomplete" for c in checks))
        self.assertTrue(all("denial cannot be inferred" in c["detail"] for c in checks))

    def test_correlation_mismatch_cannot_establish_reachability(self):
        def wrong(url, config, **kwargs):
            code, raw, _, location = self.response(url, config, **kwargs)
            return code, raw, "wrong-request", location
        with patch.object(probe, "request", side_effect=wrong):
            checks = probe.run(self.config)
        self.assertTrue(all(c["status"] == "fail" for c in checks))

    def test_late_transport_failure_preserves_confirmed_route_failure(self):
        def late_error(url, config, **kwargs):
            if kwargs.get("endpoint") is False:
                raise ConnectionResetError("later request failed")
            return self.response(url, config, **kwargs)
        with patch.object(probe, "request", side_effect=late_error):
            result = {c["name"]: c for c in probe.run(self.config)}
        self.assertEqual(result["forbidden-target-port"]["status"], "fail")
        self.assertEqual(result["endpoint-authorization"]["status"], "incomplete")


if __name__ == "__main__":
    unittest.main()
