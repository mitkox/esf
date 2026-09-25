"""Generate and optimize implementation briefs against real factory runs."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import tomllib
import uuid
from dataclasses import dataclass
from pathlib import Path

import dspy
from pydantic import BaseModel, Field


class ImplementationBrief(BaseModel):
    objective: str = Field(min_length=1)
    acceptance_criteria: list[str] = Field(min_length=1)
    constraints: list[str] = Field(default_factory=list)
    verification_focus: list[str] = Field(default_factory=list)


class DraftBrief(dspy.Signature):
    """Write a concise implementation brief grounded in the task and supplied repository context.

    Preserve the original task's intent. Do not invent requirements or claim that
    repository facts were inspected beyond the supplied context.
    """

    task: str = dspy.InputField()
    repository_context: str = dspy.InputField()
    brief: ImplementationBrief = dspy.OutputField()


def render_task(task: str, brief: ImplementationBrief) -> str:
    sections = ["Original task (authoritative):", task, "", "Implementation brief (advisory):",
                f"Objective: {brief.objective}", "Acceptance criteria:"]
    sections.extend(f"- {item}" for item in brief.acceptance_criteria)
    for label, items in (("Constraints", brief.constraints), ("Verification focus", brief.verification_focus)):
        if items:
            sections.append(label + ":")
            sections.extend(f"- {item}" for item in items)
    return "\n".join(sections) + "\n"


@dataclass(frozen=True)
class Fixture:
    id: str
    task: str
    repository_context: str
    local_path: Path
    revision: str
    agent: str
    verification: str
    acceptance_gates: tuple[str, ...]


def load_fixtures(path: Path) -> list[Fixture]:
    fixtures = []
    ids = set()
    tasks = set()
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        row = json.loads(line)
        required = {"id", "task", "repository_context", "local_path", "revision", "agent", "verification", "acceptance_gates"}
        if not isinstance(row, dict) or set(row) != required:
            raise ValueError(f"fixture line {number} has invalid fields")
        if not all(isinstance(row[key], str) and row[key].strip() for key in required - {"acceptance_gates"}):
            raise ValueError(f"fixture line {number} has an empty field")
        if not isinstance(row["acceptance_gates"], list) or not row["acceptance_gates"] or not all(isinstance(gate, str) and gate for gate in row["acceptance_gates"]):
            raise ValueError(f"fixture line {number} requires acceptance gates")
        task_id = hashlib.sha256(row["task"].strip().encode()).hexdigest()
        if row["id"] in ids or task_id in tasks or not Path(row["local_path"]).is_absolute() or not Path(row["local_path"]).is_dir():
            raise ValueError(f"fixture line {number} has a duplicate ID/task or invalid local repository")
        if not re.fullmatch(r"[0-9a-fA-F]{40}", row["revision"]):
            raise ValueError(f"fixture line {number} requires a full commit SHA")
        ids.add(row["id"])
        tasks.add(task_id)
        fixtures.append(Fixture(row["id"], row["task"], row["repository_context"],
                                Path(row["local_path"]), row["revision"], row["agent"],
                                row["verification"], tuple(row["acceptance_gates"])))
    if len(fixtures) < 4:
        raise ValueError("at least four distinct fixture tasks are required")
    return fixtures


def split_fixtures(fixtures: list[Fixture]) -> tuple[list[Fixture], list[Fixture]]:
    ordered = sorted(fixtures, key=lambda item: hashlib.sha256(item.id.encode()).hexdigest())
    held_out_count = max(1, len(ordered) // 4)
    return ordered[held_out_count:], ordered[:held_out_count]


def score_manifest(manifest: dict, verification: dict, acceptance_gates: tuple[str, ...]) -> tuple[float, str]:
    steps = {step["id"]: step for step in verification.get("steps", [])}
    failed = [gate for gate in acceptance_gates if gate not in steps or not steps[gate].get("passed")]
    if manifest.get("factory_result") != "SUCCEEDED" or manifest.get("verification_result") != "SUCCESS" or failed:
        return 0.0, "Factory or acceptance gates failed: " + ", ".join(failed)
    if manifest.get("human_result") == "rejected":
        return 0.0, "Human reviewer rejected the change"
    duration_seconds = float(manifest.get("duration", 0)) / 1e9
    cost = manifest.get("inference_cost")
    duration_penalty = min(0.1, duration_seconds / 3600 * 0.1)
    cost_penalty = min(0.1, float(cost) / 5 * 0.1) if cost is not None else 0.0
    score = 1.0 - duration_penalty - cost_penalty
    return score, f"Acceptance gates passed; duration={duration_seconds:.1f}s; cost={'unknown' if cost is None else cost}"


class FactoryRunner:
    def __init__(self, binary: Path, config: Path, max_runs: int):
        if not binary.is_file() or not config.is_file() or max_runs < 1:
            raise ValueError("factory binary, config, and positive run budget are required")
        self.binary, self.config, self.max_runs = binary, config, max_runs
        parsed = tomllib.loads(config.read_text(encoding="utf-8"))
        if parsed.get("intake", {}).get("enabled", False):
            raise ValueError("disable intake on the factory worker before running the brief lab; generated briefs may contain repository context")
        self.data_dir = Path(os.environ.get("FACTORY_DATA_DIR") or parsed["storage"]["data_dir"]).resolve()
        names = {"PATH", "HOME", "LANG", "LC_ALL", "FACTORY_DATA_DIR", "TEMPORAL_HOST_PORT",
                 "TEMPORAL_NAMESPACE", "TEMPORAL_TASK_QUEUE", "CUBE_API_KEY",
                 "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"}
        temporal_key_env = parsed.get("temporal", {}).get("api_key_env")
        if temporal_key_env:
            names.add(temporal_key_env)
        # The lab's generative-model credential is for DSPy only. Do not pass it
        # to the factory CLI, which could otherwise inherit and expose it.
        self.factory_env = {name: os.environ[name] for name in names if name in os.environ}
        self.cache: dict[tuple[str, str], tuple[float, str]] = {}
        self.calls = 0

    def run(self, fixture: Fixture, task: str) -> tuple[float, str]:
        key = (fixture.id, hashlib.sha256(task.encode()).hexdigest())
        if key in self.cache:
            return self.cache[key]
        if self.calls >= self.max_runs:
            raise RuntimeError("factory run budget exhausted")
        self.calls += 1
        run_id = "brief-lab-" + uuid.uuid4().hex
        with tempfile.TemporaryDirectory(prefix="esf-brief-") as directory:
            task_file = Path(directory) / "task.txt"
            task_file.write_text(task, encoding="utf-8")
            task_file.chmod(0o600)
            argv = [str(self.binary), "--config", str(self.config), "run",
                    "--local-path", str(fixture.local_path), "--rev", fixture.revision,
                    "--task-file", str(task_file), "--agent", fixture.agent,
                    "--verification", fixture.verification, "--run-id", run_id,
                    "--review=false", "--wait=true"]
            # The CLI may return nonzero for an ordinary failed factory run.
            try:
                subprocess.run(argv, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                               check=False, timeout=7200, env=self.factory_env)
            except subprocess.TimeoutExpired as exc:
                raise RuntimeError(f"factory run {run_id} exceeded two hours; inspect its workflow") from exc
        run_dir = self.data_dir / "runs" / run_id
        manifest_path = run_dir / "run.json"
        if not manifest_path.is_file():
            raise RuntimeError(f"factory run {run_id} has no manifest; inspect Temporal before retrying")
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        if manifest.get("factory_result") == "INFRASTRUCTURE_FAILED":
            raise RuntimeError(f"factory infrastructure failed in run {run_id}; stop the optimization")
        verification_path = run_dir / "verification" / "result.json"
        verification = json.loads(verification_path.read_text(encoding="utf-8")) if verification_path.is_file() else {}
        outcome = score_manifest(manifest, verification, fixture.acceptance_gates)
        patch = run_dir / manifest.get("patch", "changes.patch")
        if outcome[0] > 0 and (not patch.is_file() or patch.stat().st_size == 0):
            outcome = (0.0, "Factory produced no patch")
        self.cache[key] = outcome
        return outcome


def evaluate(program, fixtures: list[Fixture], runner: FactoryRunner) -> dict:
    scores = []
    for fixture in fixtures:
        prediction = program(task=fixture.task, repository_context=fixture.repository_context)
        scores.append(runner.run(fixture, render_task(fixture.task, prediction.brief))[0])
    return {"cases": len(scores), "mean_score": sum(scores) / len(scores), "scores": scores}


def evaluate_direct(fixtures: list[Fixture], runner: FactoryRunner) -> dict:
    scores = [runner.run(fixture, fixture.task)[0] for fixture in fixtures]
    return {"cases": len(scores), "mean_score": sum(scores) / len(scores), "scores": scores}


def optimize(fixtures: list[Fixture], runner: FactoryRunner, output: Path, max_metric_calls: int) -> dict:
    if output.suffix != ".json":
        raise ValueError("optimized program output must be a JSON file")
    train, held_out = split_fixtures(fixtures)
    if max_metric_calls < 1 or runner.max_runs < max_metric_calls + 3 * len(held_out):
        raise ValueError("max-runs must cover optimizer calls plus three holdout variants")
    program = dspy.Predict(DraftBrief)

    by_id = {fixture.id: fixture for fixture in fixtures}
    def metric(example, prediction, trace=None, pred_name=None, pred_trace=None, program_trace=None):
        fixture = by_id[example.fixture_id]
        score, feedback = runner.run(fixture, render_task(fixture.task, prediction.brief))
        return dspy.Prediction(score=score, feedback=feedback)

    train_examples = [dspy.Example(fixture_id=f.id, task=f.task, repository_context=f.repository_context).with_inputs("task", "repository_context") for f in train]
    optimizer = dspy.GEPA(metric=metric, max_metric_calls=max_metric_calls,
                          reflection_minibatch_size=2, num_threads=1, seed=17)
    # GEPA may use its valset for candidate selection; keep the final holdout unseen.
    tuned = optimizer.compile(program, trainset=train_examples, valset=train_examples)
    direct = evaluate_direct(held_out, runner)
    baseline = evaluate(program, held_out, runner)
    candidate = evaluate(tuned, held_out, runner)
    promotable = candidate["mean_score"] > max(direct["mean_score"], baseline["mean_score"])
    if promotable:
        output.parent.mkdir(parents=True, exist_ok=True)
        fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        os.close(fd)
        try:
            tuned.save(str(output))
        except BaseException:
            output.unlink(missing_ok=True)
            raise
    return {"promotable": promotable, "direct": direct, "baseline_brief": baseline, "candidate": candidate,
            "factory_runs": runner.calls, "program_path": str(output) if promotable else None}


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="python -m esf_brief_lab")
    parser.add_argument("--model", required=True, help="Generative DSPy model ID; provider key comes from the environment")
    sub = parser.add_subparsers(dest="command", required=True)
    generate = sub.add_parser("generate")
    generate.add_argument("--task-file", type=Path, required=True)
    generate.add_argument("--context-file", type=Path, required=True)
    generate.add_argument("--program", type=Path)
    generate.add_argument("--output", type=Path, required=True)
    optimize_cmd = sub.add_parser("optimize")
    optimize_cmd.add_argument("--fixtures", type=Path, required=True)
    optimize_cmd.add_argument("--factory", type=Path, required=True)
    optimize_cmd.add_argument("--factory-config", type=Path, required=True)
    optimize_cmd.add_argument("--output", type=Path, required=True)
    optimize_cmd.add_argument("--max-runs", type=int, default=24)
    optimize_cmd.add_argument("--max-metric-calls", type=int, default=16)
    args = parser.parse_args(argv)
    try:
        dspy.configure(lm=dspy.LM(args.model))
        if args.command == "generate":
            program = dspy.Predict(DraftBrief)
            if args.program:
                if args.program.suffix != ".json":
                    raise ValueError("brief program must be a JSON file")
                program.load(str(args.program))
            task = args.task_file.read_text(encoding="utf-8")
            context = args.context_file.read_text(encoding="utf-8")
            prompt = render_task(task, program(task=task, repository_context=context).brief)
            with os.fdopen(os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w", encoding="utf-8") as stream:
                stream.write(prompt)
            result = {"task_file": str(args.output)}
        else:
            fixtures = load_fixtures(args.fixtures)
            runner = FactoryRunner(args.factory, args.factory_config, args.max_runs)
            result = optimize(fixtures, runner, args.output, args.max_metric_calls)
        print(json.dumps(result, separators=(",", ":")))
        return 0
    except Exception as exc:
        print(f"{type(exc).__name__}: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
