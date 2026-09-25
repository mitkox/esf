import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from esf_brief_lab.lab import (
    FactoryRunner, Fixture, ImplementationBrief, load_fixtures, render_task,
    score_manifest, split_fixtures,
)


class BriefLabTests(unittest.TestCase):
    def test_brief_preserves_original_task(self):
        task = "Fix the greeting without changing its public API."
        brief = ImplementationBrief(objective="Correct output", acceptance_criteria=["Greeting test passes"])
        rendered = render_task(task, brief)
        self.assertIn(task, rendered)
        self.assertIn("Original task (authoritative)", rendered)
        self.assertIn("Greeting test passes", rendered)

    def test_acceptance_gates_determine_eligibility(self):
        manifest = {"factory_result": "SUCCEEDED", "verification_result": "SUCCESS", "duration": 1_000_000_000}
        verification = {"steps": [{"id": "build", "passed": True}, {"id": "acceptance", "passed": False}]}
        score, _ = score_manifest(manifest, verification, ("acceptance",))
        self.assertEqual(score, 0)
        verification["steps"][1]["passed"] = True
        score, _ = score_manifest(manifest, verification, ("acceptance",))
        self.assertGreater(score, 0.8)
        manifest["human_result"] = "rejected"
        self.assertEqual(score_manifest(manifest, verification, ("acceptance",))[0], 0)

    def test_fixture_split_keeps_holdout_separate(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "fixtures.jsonl"
            rows = [dict(id=f"task-{i}", task=f"Task {i}", repository_context="Python project",
                         local_path=directory, revision="a" * 40, agent="test", verification="default",
                         acceptance_gates=["acceptance"]) for i in range(4)]
            path.write_text("\n".join(json.dumps(row) for row in rows))
            fixtures = load_fixtures(path)
            train, held_out = split_fixtures(fixtures)
            self.assertEqual((len(train), len(held_out)), (3, 1))
            self.assertFalse(set(x.id for x in train) & set(x.id for x in held_out))
            rows[1]["task"] = rows[0]["task"]
            path.write_text("\n".join(json.dumps(row) for row in rows))
            with self.assertRaisesRegex(ValueError, "duplicate ID/task"):
                load_fixtures(path)

    def test_factory_runner_rejects_enabled_intake_for_generated_briefs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / "fake-factory"
            binary.write_text("#!/bin/sh\nexit 0\n")
            config = root / "factory.toml"
            config.write_text('[storage]\ndata_dir = ".factory"\n[intake]\nenabled = true\n')
            with self.assertRaisesRegex(ValueError, "disable intake"):
                FactoryRunner(binary, config, 1)

    def test_factory_runner_uses_task_file_and_caps_runs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = root / "data"
            config = root / "factory.toml"
            config.write_text(f'[storage]\ndata_dir = "{data}"\n')
            binary = root / "fake-factory"
            binary.write_text("#!/bin/sh\nexit 1\n")
            binary.chmod(0o700)
            fixture = Fixture("one", "Task", "Context", root, "a" * 40, "test", "default", ("acceptance",))
            with patch.dict(os.environ, {"FACTORY_DATA_DIR": str(data), "OPENAI_API_KEY": "sentinel-private-key"}):
                runner = FactoryRunner(binary, config, 1)
                self.assertNotIn("OPENAI_API_KEY", runner.factory_env)
                with self.assertRaisesRegex(RuntimeError, "has no manifest"):
                    runner.run(fixture, "Task and brief")
                with self.assertRaisesRegex(RuntimeError, "budget exhausted"):
                    runner.run(fixture, "Different task and brief")

    def test_factory_runner_scores_persisted_gate_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = root / "data"
            config = root / "factory.toml"
            config.write_text(f'[storage]\ndata_dir = "{data}"\n')
            binary = root / "fake-factory"
            binary.write_text("""#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
args = sys.argv
run = args[args.index('--run-id') + 1]
task = Path(args[args.index('--task-file') + 1]).read_text()
assert 'Original task' in task
directory = Path(os.environ['FACTORY_DATA_DIR']) / 'runs' / run
(directory / 'verification').mkdir(parents=True)
(directory / 'run.json').write_text(json.dumps({
    'factory_result': 'SUCCEEDED', 'verification_result': 'SUCCESS',
    'duration': 1_000_000_000, 'inference_cost': 0.01, 'patch': 'changes.patch',
}))
(directory / 'changes.patch').write_text('diff --git a/file b/file\\n')
(directory / 'verification' / 'result.json').write_text(json.dumps({
    'steps': [{'id': 'acceptance', 'passed': True}],
}))
""")
            binary.chmod(0o700)
            fixture = Fixture("one", "Task", "Context", root, "a" * 40, "test", "default", ("acceptance",))
            with patch.dict(os.environ, {"FACTORY_DATA_DIR": str(data)}):
                runner = FactoryRunner(binary, config, 2)
                score, feedback = runner.run(fixture, "Original task (authoritative): Task")
                self.assertGreater(score, 0.8)
                self.assertIn("Acceptance gates passed", feedback)
                self.assertEqual(runner.run(fixture, "Original task (authoritative): Task")[0], score)
                self.assertEqual(runner.calls, 1)

    def test_factory_runner_rejects_no_patch_success(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = root / "data"
            config = root / "factory.toml"
            config.write_text(f'[storage]\ndata_dir = "{data}"\n')
            binary = root / "fake-factory"
            binary.write_text("""#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
args = sys.argv
run = args[args.index('--run-id') + 1]
directory = Path(os.environ['FACTORY_DATA_DIR']) / 'runs' / run
directory.mkdir(parents=True)
(directory / 'verification').mkdir()
(directory / 'run.json').write_text(json.dumps({
    'factory_result': 'SUCCEEDED', 'verification_result': 'SUCCESS',
    'patch': 'changes.patch',
}))
(directory / 'changes.patch').write_text('')
(directory / 'verification' / 'result.json').write_text(json.dumps({
    'steps': [{'id': 'acceptance', 'passed': True}],
}))
""")
            binary.chmod(0o700)
            fixture = Fixture("one", "Task", "Context", root, "a" * 40, "test", "default", ("acceptance",))
            with patch.dict(os.environ, {"FACTORY_DATA_DIR": str(data)}):
                score, feedback = FactoryRunner(binary, config, 1).run(fixture, "Task")
            self.assertEqual(score, 0)
            self.assertIn("no patch", feedback)


if __name__ == "__main__":
    unittest.main()
