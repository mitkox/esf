"""Small boundary suite for the new release acceptance gate; fixtures are synthetic."""

import copy
import json
from pathlib import Path
import subprocess
import shutil
import sys
import tempfile
import tarfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from release_qualification import GATES, PLATFORMS, TARGETS, sha256, verify_qualification


class QualificationBoundaryTests(unittest.TestCase):
    def test_exact_candidate_and_fail_closed_boundaries(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            inventory = {"release": "v0.6.0", "images": {"factory": None},
                         "templates": {"recipe_sha256": "a" * 64}}
            (root / "release").mkdir()
            (root / "release/inventory.json").write_text(json.dumps(inventory))
            (root / ".gitignore").write_text("*\n!release/\n!release/inventory.json\n!.gitignore\n")
            subprocess.run(["git", "-C", str(root), "add", "."], check=True)
            subprocess.run(["git", "-C", str(root), "-c", "user.name=Test",
                            "-c", "user.email=test@example.invalid", "commit",
                            "-qm", "fixture"], check=True)
            commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
            assets = root / "assets"
            assets.mkdir()
            release = {"release": "v0.6.0-rc.1", "binary_release": "v0.6.0", "commit": commit}
            (assets / "release-manifest.json").write_text(json.dumps(release))
            (assets / "dependency-inventory.json").write_bytes((root / "release/inventory.json").read_bytes())
            for component in ("factory", "machinist", "combined"):
                for platform in PLATFORMS:
                    (assets / f"esf_{component}_0.6.0-rc.1_{platform}.tar.gz").write_bytes(b"synthetic boundary fixture")
            for name in ("sbom.cdx.json", "checksums.txt"):
                (assets / name).write_text("synthetic boundary fixture")
            (root / "evidence").mkdir()
            (root / "evidence/results.json").write_text("synthetic boundary fixture")
            refs = ["evidence/results.json"]
            gates = {name: {"status": "passed", "evidence": refs} for name in GATES}
            gates["soak_24h"]["targets"] = {target: {"duration_seconds": 86400, "unexpected_failures": 0,
                                                    "unreconciled_sandboxes": 0} for target in TARGETS}
            trial = {"batches": 30, "unexpected_failures": 0, "unreconciled_sandboxes": 0,
                     "baseline_p95_duration_seconds": 10, "candidate_p95_duration_seconds": 12,
                     "baseline_peak_worker_rss_bytes": 100, "candidate_peak_worker_rss_bytes": 120}
            gates["performance"]["targets"] = {target: {c: copy.deepcopy(trial) for c in ("1", "4", "8")}
                                               for target in TARGETS}
            good = {"schema": "esf-qualification/v1", "release": "v0.6.0", "commit": commit,
                    "inventory_sha256": sha256(root / "release/inventory.json"),
                    "artifacts": {file.name: sha256(file) for file in assets.iterdir()},
                    "evidence": {refs[0]: sha256(root / refs[0])}, "gates": gates,
                    "images": {"factory": {"digest": "sha256:" + "b" * 64,
                                           "sboms": {"linux/amd64": refs, "linux/arm64": refs},
                                           "platforms": {p: {"digest": "sha256:" + "d" * 64,
                                                             "security_evidence": refs}
                                                         for p in ("linux/amd64", "linux/arm64")}}},
                    "template": {"id": "fixture", "sha256": "c" * 64, "recipe_sha256": "a" * 64},
                    "transitive_licenses": "reviewed"}
            manifest_path = root / "qualification-manifest.json"

            def verify(value):
                manifest_path.write_text(json.dumps(value))
                return verify_qualification(root, inventory, manifest_path, assets)

            self.assertEqual(verify(good), [])
            scripts = root / "scripts"
            scripts.mkdir()
            for name in ("promote-release.py", "release_qualification.py"):
                shutil.copyfile(Path(__file__).resolve().parents[1] / name, scripts / name)
            promoted = root / "promoted"
            subprocess.run([sys.executable, str(scripts / "promote-release.py"), "v0.6.0-rc.1", "v0.6.0",
                            str(assets), str(promoted), str(manifest_path)], cwd=root, check=True, capture_output=True)
            for archive in assets.glob("esf_*.tar.gz"):
                self.assertEqual(archive.read_bytes(), (promoted / archive.name.replace("_0.6.0-rc.1_", "_0.6.0_")).read_bytes())
            with tarfile.open(promoted / "qualification-evidence.tar.gz") as bundle:
                self.assertEqual(set(bundle.getnames()), {"qualification-manifest.json", refs[0]})
                self.assertEqual(bundle.extractfile(refs[0]).read(), (root / refs[0]).read_bytes())
            mutations = {
                "stale source": lambda m: m.update(commit="0" * 40),
                "missing gate": lambda m: m["gates"].pop("vm"),
                "pending gate": lambda m: m["gates"]["security"].update(status="pending"),
                "short soak": lambda m: m["gates"]["soak_24h"]["targets"]["vm"].update(duration_seconds=86399),
                "regression": lambda m: m["gates"]["performance"]["targets"]["vm"]["8"].update(candidate_p95_duration_seconds=12.1),
                "missing platform": lambda m: m["images"]["factory"]["sboms"].pop("linux/arm64"),
                "unbound evidence": lambda m: m["gates"]["restore"].update(evidence=["evidence/missing.json"]),
                "bad evidence hash": lambda m: m["evidence"].update({refs[0]: "0" * 64}),
                "malformed gates": lambda m: m.update(gates=None),
                "missing archive": lambda m: m["artifacts"].pop("esf_factory_0.6.0-rc.1_linux_amd64.tar.gz"),
            }
            for name, mutate in mutations.items():
                with self.subTest(name=name):
                    candidate = copy.deepcopy(good)
                    mutate(candidate)
                    self.assertTrue(verify(candidate))
            evidence = root / refs[0]
            evidence.unlink()
            evidence.symlink_to(assets / "checksums.txt")
            changed = copy.deepcopy(good)
            changed["evidence"][refs[0]] = sha256(evidence)
            self.assertTrue(verify(changed), "symlink evidence must be rejected")
            self.assertTrue(verify_qualification(root, inventory, root / "absent.json", assets))
            (root / "release/inventory.json").write_text("changed source")
            self.assertTrue(verify(good), "dirty source must be rejected")


if __name__ == "__main__":
    unittest.main()
