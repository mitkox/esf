"""Fit decision boundaries from operator labels; never deploy automatically."""

from __future__ import annotations

import hashlib
import json
import os
import stat
import fcntl
from collections import defaultdict
from pathlib import Path

import dspy
from dspy.experimental import ReAnchor

from .decision import predictor

KINDS = {"bugfix", "feature", "refactor", "docs", "other"}
AMBIGUITIES = {"clear", "partial", "ambiguous"}
RUBRIC = ("clear", "partial", "ambiguous")


def append_label(run_dir: Path, labels_path: Path, ready: bool, task_type: str, ambiguity: str) -> dict:
    if not labels_path.is_absolute() or task_type not in KINDS or ambiguity not in AMBIGUITIES:
        raise ValueError("labels path must be absolute and labels must use the declared categories")
    task_record = json.loads((run_dir / "task.json").read_text(encoding="utf-8"))
    run_id, task = task_record["run_id"], task_record["task"]
    if not isinstance(run_id, str) or not run_id or not isinstance(task, str) or not task.strip():
        raise ValueError("task artifact lacks run ID or task text")
    labels_path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd = os.open(labels_path, os.O_RDWR | os.O_CREAT, 0o600)
    try:
        with os.fdopen(fd, "r+", encoding="utf-8") as stream:
            fcntl.flock(stream, fcntl.LOCK_EX)
            if stat.S_IMODE(os.fstat(stream.fileno()).st_mode) & 0o077:
                raise ValueError("labels file must be inaccessible to group and others")
            for line in stream:
                if line.strip():
                    old = json.loads(line)
                    if old.get("run_id") == run_id or old.get("task") == task:
                        raise ValueError("run or task is already labeled")
            record = {"run_id": run_id, "task": task, "ready": ready,
                      "task_type": task_type, "ambiguity": ambiguity}
            stream.write(json.dumps(record, separators=(",", ":")) + "\n")
            stream.flush()
            os.fsync(stream.fileno())
            return {"run_id": run_id, "labels_path": str(labels_path)}
    except BaseException:
        # fdopen owns the descriptor after it succeeds; close only when opening fails.
        try:
            os.close(fd)
        except OSError:
            pass
        raise


def read_labels(path: Path) -> list[dict]:
    if not path.is_file():
        raise ValueError("labels file does not exist")
    if stat.S_IMODE(path.stat().st_mode) & 0o077:
        raise ValueError("labels file must be inaccessible to group and others")
    rows = []
    seen = set()
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        row = json.loads(line)
        if not isinstance(row, dict) or set(row) != {"run_id", "task", "ready", "task_type", "ambiguity"}:
            raise ValueError(f"label line {line_number} has invalid fields")
        if not isinstance(row["run_id"], str) or not row["run_id"] or not isinstance(row["task"], str) or not row["task"].strip():
            raise ValueError(f"label line {line_number} lacks run ID or task")
        if type(row["ready"]) is not bool or row["task_type"] not in KINDS or row["ambiguity"] not in AMBIGUITIES:
            raise ValueError(f"label line {line_number} has invalid labels")
        task_id = hashlib.sha256(row["task"].encode()).hexdigest()
        if task_id in seen:
            raise ValueError(f"label line {line_number} repeats a task")
        seen.add(task_id)
        rows.append(row)
    if len(rows) < 40 or sum(row["ready"] for row in rows) < 10 or sum(not row["ready"] for row in rows) < 10:
        raise ValueError("at least 40 distinct labels, including 10 of each readiness class, are required")
    return rows


def split_labels(rows: list[dict]) -> tuple[list[dict], list[dict]]:
    by_class = defaultdict(list)
    for row in rows:
        by_class[row["ready"]].append(row)
    train, held_out = [], []
    for klass in (False, True):
        group = sorted(by_class[klass], key=lambda row: hashlib.sha256(row["task"].encode()).hexdigest())
        count = max(1, len(group) // 4)
        held_out.extend(group[:count])
        train.extend(group[count:])
    return train, held_out


def metric(example, prediction, trace=None) -> float:
    predicted_ready = bool(prediction.ready.value)
    if predicted_ready == example.ready:
        readiness = 1.0
    elif not predicted_ready:
        readiness = 0.4  # False alarms cost less than saying an unclear task is ready.
    else:
        readiness = 0.0
    return (0.7 * readiness + 0.2 * (prediction.task_type.value == example.task_type)
            + 0.1 * (RUBRIC[prediction.ambiguity.level] == example.ambiguity))


def report(program, rows: list[dict]) -> dict:
    scores = []
    false_ready = 0
    false_not_ready = 0
    type_correct = 0
    ambiguity_correct = 0
    for row in rows:
        prediction = program(task=row["task"])
        example = dspy.Example(**row).with_inputs("task")
        scores.append(metric(example, prediction))
        false_ready += bool(prediction.ready.value) and not row["ready"]
        false_not_ready += not bool(prediction.ready.value) and row["ready"]
        type_correct += prediction.task_type.value == row["task_type"]
        ambiguity_correct += RUBRIC[prediction.ambiguity.level] == row["ambiguity"]
    return {
        "cases": len(rows),
        "metric": sum(scores) / len(scores),
        "false_ready": false_ready,
        "false_not_ready": false_not_ready,
        "task_type_accuracy": type_correct / len(rows),
        "ambiguity_accuracy": ambiguity_correct / len(rows),
    }


def calibrate(labels_path: Path, output_path: Path) -> dict:
    if output_path.suffix != ".json":
        raise ValueError("calibrated program output must be a JSON file")
    rows = read_labels(labels_path)
    train, held_out = split_labels(rows)
    program, _ = predictor()
    baseline = report(program, held_out)
    optimizer = ReAnchor(metric=metric)
    tuned = optimizer.compile(
        program,
        trainset=[dspy.Example(**row).with_inputs("task") for row in train],
        valset=[dspy.Example(**row).with_inputs("task") for row in held_out],
    )
    candidate = report(tuned, held_out)
    if candidate["metric"] <= baseline["metric"] or candidate["false_ready"] > baseline["false_ready"]:
        return {"promotable": False, "baseline": baseline, "candidate": candidate}
    output_path.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(output_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    os.close(fd)
    try:
        tuned.save(str(output_path))
    except BaseException:
        output_path.unlink(missing_ok=True)
        raise
    return {"promotable": True, "baseline": baseline, "candidate": candidate,
            "program_path": str(output_path)}
