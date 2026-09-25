"""Machine-readable assessment and operator-run calibration commands."""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path

from .decision import assess, configure, predictor


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="python -m esf_intake")
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("assess")
    labeling = sub.add_parser("label")
    labeling.add_argument("--run-dir", type=Path, required=True)
    labeling.add_argument("--labels", type=Path, required=True)
    labeling.add_argument("--ready", choices=("yes", "no"), required=True)
    labeling.add_argument("--task-type", choices=("bugfix", "feature", "refactor", "docs", "other"), required=True)
    labeling.add_argument("--ambiguity", choices=("clear", "partial", "ambiguous"), required=True)
    calibration = sub.add_parser("calibrate")
    calibration.add_argument("--labels", type=Path, required=True)
    calibration.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)

    try:
        if args.command == "label":
            from .calibrate import append_label

            result = append_label(args.run_dir, args.labels, args.ready == "yes", args.task_type, args.ambiguity)
            print(json.dumps(result, separators=(",", ":")))
            return 0
        key_file = Path(os.environ["TYPESAFE_API_KEY_FILE"])
        model = os.environ.get("ESF_INTAKE_MODEL", "jev-latest")
        configure(key_file, model)
        if args.command == "assess":
            request = json.load(sys.stdin)
            if not isinstance(request, dict) or not isinstance(request.get("task"), str) or not request["task"].strip():
                raise ValueError("request must contain a nonempty task string")
            path = os.environ.get("ESF_INTAKE_PROGRAM")
            program, program_id = predictor(Path(path) if path else None)
            result = assess(request["task"], program, program_id)
        else:
            from .calibrate import calibrate

            result = calibrate(args.labels, args.output)
        print(json.dumps(result, allow_nan=False, separators=(",", ":")))
        return 0
    except Exception as exc:
        # The error may originate from a provider that echoes request data.
        # The worker discards stderr and records only a generic error code.
        print(type(exc).__name__, file=sys.stderr)
        return 1
