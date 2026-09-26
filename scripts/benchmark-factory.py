#!/usr/bin/env python3
"""Measure a disposable factory fixture at 1, 4, and 8 concurrent runs."""

from __future__ import annotations

import argparse
from datetime import datetime
import json
import math
from pathlib import Path
import subprocess
import time
import uuid


TASK = 'Change the greeting from "hello" to "hello factory". Update the tests appropriately so they pass.'


def percentile(values: list[float], fraction: float) -> float:
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * fraction) - 1)]


def rss_bytes(pid: int) -> int:
    try:
        for line in Path(f"/proc/{pid}/status").read_text().splitlines():
            if line.startswith("VmRSS:"):
                return int(line.split()[1]) * 1024
    except FileNotFoundError:
        pass
    return 0


def timestamp(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def evidence_bytes(path: Path) -> int:
    return sum(item.stat().st_size for item in path.rglob("*") if item.is_file())


def start_worker(factory: Path, config: Path, output: Path) -> tuple[subprocess.Popen, object]:
    worker_log = (output / "worker.log").open("w")
    worker = subprocess.Popen(
        [str(factory), "worker", "--config", str(config)],
        stdout=worker_log, stderr=subprocess.STDOUT,
    )
    for _ in range(300):
        if worker.poll() is not None:
            raise RuntimeError(f"factory worker exited during startup; see {output / 'worker.log'}")
        if "factory worker starting" in (output / "worker.log").read_text():
            return worker, worker_log
        time.sleep(0.1)
    raise RuntimeError(f"factory worker did not start; see {output / 'worker.log'}")


def batch(factory: Path, config: Path, repository: Path, revision: str,
          data_dir: Path, output: Path, worker: subprocess.Popen, count: int) -> dict:
    processes: list[tuple[str, subprocess.Popen, object]] = []
    started = time.monotonic()
    for _ in range(count):
        run_id = f"bench-{uuid.uuid4().hex[:20]}"
        log = (output / f"{run_id}.log").open("w")
        process = subprocess.Popen([
            str(factory), "run", "--config", str(config),
            "--run-id", run_id, "--local-path", str(repository),
            "--rev", revision, "--task", TASK,
            "--agent", "conformance", "--verification", "default", "--wait",
        ], stdout=log, stderr=subprocess.STDOUT)
        processes.append((run_id, process, log))

    peak_worker_rss = 0
    while any(process.poll() is None for _, process, _ in processes):
        peak_worker_rss = max(peak_worker_rss, rss_bytes(worker.pid))
        if worker.poll() is not None:
            raise RuntimeError("factory worker exited during the benchmark")
        time.sleep(0.1)
    wall_seconds = time.monotonic() - started
    peak_worker_rss = max(peak_worker_rss, rss_bytes(worker.pid))

    runs = []
    for run_id, process, log in processes:
        log.close()
        manifest_path = data_dir / "runs" / run_id / "run.json"
        if process.wait() != 0 or not manifest_path.exists():
            raise RuntimeError(f"{run_id} failed; see {output / (run_id + '.log')}")
        manifest = json.loads(manifest_path.read_text())
        conditions = {item["type"]: item for item in manifest["conditions"]}
        agent_message = conditions["AgentCompleted"].get("message", "")
        if (manifest["factory_result"] != "SUCCEEDED"
                or manifest["verification_result"] != "SUCCESS"
                or not manifest["cleanup_result"]["verified"]
                or "attempt=1" not in agent_message):
            raise RuntimeError(f"{run_id} has a failed gate or repeated agent execution")
        start = timestamp(manifest["started_at"])
        ready = timestamp(conditions["SandboxReady"]["last_transition_time"])
        repository_ready = timestamp(conditions["RepositoryPrepared"]["last_transition_time"])
        runs.append({
            "run_id": run_id,
            "duration_seconds": manifest["duration"] / 1e9,
            "sandbox_ready_seconds": (ready - start).total_seconds(),
            "repository_ready_seconds": (repository_ready - start).total_seconds(),
            "evidence_bytes": evidence_bytes(manifest_path.parent),
            "agent_attempts": 1,
            "cleanup_verified": True,
        })
    sandbox_check = subprocess.run(
        [str(factory), "sandboxes", "--config", str(config)],
        capture_output=True, text=True, check=False,
    )
    if sandbox_check.returncode != 0:
        raise RuntimeError(f"sandbox leak after batch {count}: {sandbox_check.stdout} {sandbox_check.stderr}")
    return {
        "concurrency": count,
        "batch_wall_seconds": wall_seconds,
        "worker_peak_rss_bytes": peak_worker_rss,
        "p50_sandbox_ready_seconds": percentile([run["sandbox_ready_seconds"] for run in runs], 0.50),
        "p95_sandbox_ready_seconds": percentile([run["sandbox_ready_seconds"] for run in runs], 0.95),
        "p50_duration_seconds": percentile([run["duration_seconds"] for run in runs], 0.50),
        "p95_duration_seconds": percentile([run["duration_seconds"] for run in runs], 0.95),
        "retained_evidence_bytes": sum(run["evidence_bytes"] for run in runs),
        "unreconciled_sandboxes": 0,
        "runs": runs,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--factory", type=Path, required=True)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--repository", type=Path, required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--data-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    worker = None
    worker_log = None
    try:
        worker, worker_log = start_worker(args.factory, args.config, args.output)
        result = {
            "fixture_revision": args.revision,
            "batches": [
                batch(args.factory, args.config, args.repository, args.revision,
                      args.data_dir, args.output, worker, count)
                for count in (1, 4, 8)
            ],
        }
        (args.output / "results.json").write_text(json.dumps(result, indent=2) + "\n")
        print(json.dumps({"output": str(args.output), "batches": result["batches"]}, indent=2))
        return 0
    finally:
        if worker is not None:
            worker.terminate()
            try:
                worker.wait(timeout=30)
            except subprocess.TimeoutExpired:
                worker.kill()
                worker.wait()
        if worker_log is not None:
            worker_log.close()


if __name__ == "__main__":
    raise SystemExit(main())
