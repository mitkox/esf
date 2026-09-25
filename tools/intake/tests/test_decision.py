import json
import io
import os
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

from dspy.experimental import Choice, Noul, Score

from esf_intake.calibrate import calibrate, read_labels, split_labels
from esf_intake.cli import main
from esf_intake.decision import AssessTask, assess, configure, predictor


class DecisionTests(unittest.TestCase):
    def test_signature_has_top_level_decision_fields(self):
        self.assertEqual(set(AssessTask.output_fields), {"ready", "task_type", "ambiguity"})
        self.assertIs(AssessTask.fields["ready"].annotation, Noul)
        self.assertTrue(issubclass(AssessTask.fields["task_type"].annotation, Choice))
        self.assertTrue(issubclass(AssessTask.fields["ambiguity"].annotation, Score))

    def test_serializes_typed_evidence(self):
        prediction = SimpleNamespace(
            ready=Noul(value=False, probability=0.25, confidence=0.5),
            task_type=Choice(value="bugfix", probabilities={
                "bugfix": 0.6, "feature": 0.1, "refactor": 0.1, "docs": 0.1, "other": 0.1,
            }, confidence=0.6),
            ambiguity=Score(value=1.5, level=2, probabilities={0: 0.1, 1: 0.3, 2: 0.6}, confidence=0.6),
        )
        program = lambda **kwargs: prediction
        result = assess("Fix the greeting", program, "intake-v1")
        self.assertFalse(result["ready"]["value"])
        self.assertEqual(result["ambiguity"]["level"], "ambiguous")
        self.assertEqual(result["ambiguity"]["probabilities"]["partial"], 0.3)
        self.assertNotIn("Fix the greeting", json.dumps(result))

    def test_key_is_read_from_owner_only_file(self):
        with tempfile.TemporaryDirectory() as directory:
            key_file = Path(directory) / "key"
            key_file.write_text("sentinel-private-key\n")
            key_file.chmod(0o600)
            with patch.dict(os.environ, {}, clear=False), patch("esf_intake.decision.TypeSafe") as client, patch("esf_intake.decision.dspy.configure"):
                configure(key_file, "jev-latest")
                self.assertEqual(os.environ["TYPESAFE_API_KEY"], "sentinel-private-key")
                client.assert_called_once_with("jev-latest")
            key_file.chmod(0o644)
            with self.assertRaises(ValueError):
                configure(key_file, "jev-latest")

    def test_program_digest_matches_loaded_bytes(self):
        with tempfile.TemporaryDirectory() as directory:
            program_file = Path(directory) / "program.json"
            program_file.write_text('{"version":1}')

            class FakeProgram:
                def load(self, path):
                    Path(path).write_text('{"version":2}')

            with patch("esf_intake.decision.dspy.Predict", return_value=FakeProgram()):
                with self.assertRaisesRegex(ValueError, "changed while loading"):
                    predictor(program_file)
            with self.assertRaisesRegex(ValueError, "JSON file"):
                predictor(program_file.with_suffix(".pkl"))

    def test_calibration_only_saves_safe_held_out_improvement(self):
        rows = [
            {"run_id": f"run-{i}", "task": f"Task {i}", "ready": i % 2 == 0,
             "task_type": "bugfix", "ambiguity": "clear"}
            for i in range(40)
        ]
        baseline = {"metric": 0.6, "false_ready": 2}

        class FakeProgram:
            def save(self, path):
                Path(path).write_text("{}")

        with tempfile.TemporaryDirectory() as directory:
            labels = Path(directory) / "labels.jsonl"
            labels.write_text("\n".join(json.dumps(row) for row in rows))
            labels.chmod(0o600)
            output = Path(directory) / "compiled-v1.json"
            with patch("esf_intake.calibrate.predictor", return_value=(object(), "intake-v1")), \
                    patch("esf_intake.calibrate.ReAnchor") as optimizer, \
                    patch("esf_intake.calibrate.report", side_effect=[baseline, {"metric": 0.7, "false_ready": 3}]):
                optimizer.return_value.compile.return_value = FakeProgram()
                self.assertFalse(calibrate(labels, output)["promotable"])
            self.assertFalse(output.exists())
            with patch("esf_intake.calibrate.predictor", return_value=(object(), "intake-v1")), \
                    patch("esf_intake.calibrate.ReAnchor") as optimizer, \
                    patch("esf_intake.calibrate.report", side_effect=[baseline, {"metric": 0.7, "false_ready": 2}]):
                optimizer.return_value.compile.return_value = FakeProgram()
                self.assertTrue(calibrate(labels, output)["promotable"])
            self.assertEqual(output.stat().st_mode & 0o777, 0o600)

    def test_labels_are_distinct_and_stratified(self):
        rows = [
            {"run_id": f"run-{i}", "task": f"Task {i}", "ready": i % 2 == 0,
             "task_type": "bugfix", "ambiguity": "clear"}
            for i in range(40)
        ]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "labels.jsonl"
            path.write_text("\n".join(json.dumps(row) for row in rows))
            path.chmod(0o600)
            loaded = read_labels(path)
            train, held_out = split_labels(loaded)
            self.assertEqual(len(train), 30)
            self.assertEqual(len(held_out), 10)
            self.assertEqual(sum(row["ready"] for row in held_out), 5)
            path.write_text(json.dumps(rows[0]))
            with self.assertRaises(ValueError):
                read_labels(path)

    def test_assess_cli_only_passes_task_to_predictor(self):
        stdin = io.StringIO(json.dumps({"task": "Fix greeting"}))
        stdout = io.StringIO()
        fake_result = {"status": "ok", "program": "intake-v1"}
        with patch.dict(os.environ, {"TYPESAFE_API_KEY_FILE": "/secure/key"}), \
                patch("sys.stdin", stdin), patch("sys.stdout", stdout), \
                patch("esf_intake.cli.configure"), \
                patch("esf_intake.cli.predictor", return_value=(object(), "intake-v1")), \
                patch("esf_intake.cli.assess", return_value=fake_result) as run:
            self.assertEqual(main(["assess"]), 0)
            run.assert_called_once()
            self.assertEqual(run.call_args.args[0], "Fix greeting")
            self.assertEqual(json.loads(stdout.getvalue()), fake_result)

    def test_label_command_reads_existing_task_artifact_without_a_key(self):
        with tempfile.TemporaryDirectory() as directory:
            run_dir = Path(directory) / "run-1"
            run_dir.mkdir()
            (run_dir / "task.json").write_text(json.dumps({"run_id": "run-1", "task": "Fix greeting"}))
            labels = Path(directory) / "labels.jsonl"
            with patch.dict(os.environ, {}, clear=True), patch("sys.stdout", io.StringIO()):
                self.assertEqual(main(["label", "--run-dir", str(run_dir), "--labels", str(labels),
                                       "--ready", "yes", "--task-type", "bugfix", "--ambiguity", "clear"]), 0)
            record = json.loads(labels.read_text())
            self.assertEqual(record["task"], "Fix greeting")
            self.assertTrue(record["ready"])
            self.assertEqual(labels.stat().st_mode & 0o777, 0o600)


if __name__ == "__main__":
    unittest.main()
