#!/bin/sh
# Deterministic CONFORMANCE agent for the end-to-end factory test.
#
# WHY THIS EXISTS
#
# The factory's contract with a coding agent is narrow and testable:
#
#	receive a task on standard input
#	modify the repository in the working directory
#	exit zero on success, non-zero on failure
#
# This script satisfies exactly that contract without a language model, so the
# end-to-end acceptance test can prove the FACTORY — sandbox lifecycle, prompt
# delivery, patch extraction, deterministic verification, cleanup — independently
# of model availability and credential provisioning.
#
# It is NOT a substitute for a real coding agent, and the factory treats it as
# an ordinary operator-registered harness with no special casing. The OpenCode
# harness is implemented alongside it and is selected with `--agent opencode2`.
#
# The task is read from stdin, exactly as the OpenCode harness delivers it, so
# the prompt-transport path is exercised too.
set -eu

# Consume the task from stdin. A real agent would act on it; this one applies a
# fixed, deterministic change so the verification gate is meaningful.
TASK="$(cat)"
if [ -z "${TASK}" ]; then
  echo "conformance agent: no task provided on stdin" >&2
  exit 2
fi
echo "conformance agent: received task (${#TASK} bytes)"

# Intake credentials belong to the worker host, never the agent sandbox.
if [ -n "${TYPESAFE_API_KEY:-}" ] || [ -n "${TYPESAFE_API_KEY_FILE:-}" ] || \
   [ -e /run/credentials/factory-worker.service/typesafe-api-key ]; then
  echo "conformance agent: intake credential reached the sandbox" >&2
  exit 4
fi

if [ ! -f greeting.py ]; then
  echo "conformance agent: greeting.py not found in $(pwd)" >&2
  exit 3
fi

# The intended change for the acceptance fixture.
python3 - <<'PY'
import pathlib

path = pathlib.Path("greeting.py")
source = path.read_text()
updated = source.replace('return "hello"', 'return "hello factory"')
if updated == source:
    raise SystemExit("conformance agent: expected greeting not found")
path.write_text(updated)
print("conformance agent: updated greeting.py")
PY

echo "conformance agent: done"
exit 0
