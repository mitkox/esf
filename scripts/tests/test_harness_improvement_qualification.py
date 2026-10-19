import copy
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from release_qualification import HARNESS2_COMMIT, verify_harness_improvement


class HarnessImprovementQualificationTests(unittest.TestCase):
    def test_disabled_feature_does_not_claim_quality(self):
        self.assertEqual(verify_harness_improvement(None, Path("."), set(), "a" * 40), [])
        self.assertEqual(verify_harness_improvement({"enabled": False}, Path("."), set(), "a" * 40), [])
        self.assertTrue(verify_harness_improvement({"enabled": "yes"}, Path("."), set(), "a" * 40))

    def test_enabled_feature_binds_all_evidence(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            profile, route, policy = ("sha256:" + x * 64 for x in "abc")
            commit = "d" * 40
            section = {"enabled": True, "mode": "adaptive", "upstream_commit": HARNESS2_COMMIT,
                       "profile_digest": profile, "route_digest": route, "policy_digest": policy}
            for key in ("quality_report", "independent_approval", "component_activation", "budget_enforcement",
                        "scope_isolation", "cleanup"):
                section[key] = key + ".json"
            refs = set(section[key] for key in ("quality_report", "independent_approval", "component_activation",
                                               "budget_enforcement", "scope_isolation", "cleanup"))
            report = {"schema": "esf-harness-quality/v1", "passed": True,
                      "input": {"source_commit": commit, "split": "qualification", "mode": "adaptive",
                                "profile": profile, "route_digest": route, "policy_digest": policy}}
            (root / section["quality_report"]).write_text(json.dumps(report))
            (root / section["independent_approval"]).write_text(json.dumps(
                {"schema": "esf-harness-approval/v1", "profile": profile, "signature": "synthetic-boundary-fixture"}))
            self.assertEqual(verify_harness_improvement(section, root, refs, commit), [])
            for key in ("upstream_commit", "profile_digest", "route_digest", "policy_digest", "quality_report"):
                bad = copy.deepcopy(section)
                bad[key] = "wrong"
                self.assertTrue(verify_harness_improvement(bad, root, refs, commit), key)
            report["input"]["split"] = "development"
            (root / section["quality_report"]).write_text(json.dumps(report))
            self.assertTrue(verify_harness_improvement(section, root, refs, commit))
            bad = copy.deepcopy(section)
            bad["quality_report"] = []
            self.assertTrue(verify_harness_improvement(bad, root, refs, commit))
            (root / section["quality_report"]).write_text("[]")
            self.assertTrue(verify_harness_improvement(section, root, refs, commit))
