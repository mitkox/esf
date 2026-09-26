#!/usr/bin/env bash
# The Phase 1 acceptance test, as one command.
#
#	one task in → one isolated Cube microVM → one coding agent →
#	one deterministic verification → one verified patch out
#
# It starts the factory worker, submits exactly one task, waits for the result,
# and then asserts the two properties that matter most:
#
#   1. a usable patch exists;
#   2. no Cube sandbox leaked.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

TASK="${TASK:-Change the greeting from \"hello\" to \"hello factory\". Update the tests appropriately so they pass.}"
AGENT="${FACTORY_AGENT:-opencode2}"
VERIFICATION="${FACTORY_VERIFICATION:-default}"
WORKER_LOG="${ROOT}/.factory/worker.log"

mkdir -p "${ROOT}/.factory"

echo "== preparing fixture repository =="
FIXTURE_OUTPUT="$(./scripts/make-e2e-repo.sh)"
FIXTURE_PATH="$(echo "${FIXTURE_OUTPUT}" | sed -n 's/^fixture_path=//p')"
FIXTURE_SHA="$(echo "${FIXTURE_OUTPUT}" | sed -n 's/^fixture_sha=//p')"
echo "   path: ${FIXTURE_PATH}"
echo "   sha:  ${FIXTURE_SHA}"
echo

echo "== building factory =="
make --no-print-directory build >/dev/null
echo "   ok"
echo

echo "== starting factory worker =="
./bin/factory worker >"${WORKER_LOG}" 2>&1 &
WORKER_PID=$!
# shellcheck disable=SC2329 # Invoked by the EXIT trap.
cleanup() {
  if kill -0 "${WORKER_PID}" 2>/dev/null; then
    kill "${WORKER_PID}" 2>/dev/null || true
    wait "${WORKER_PID}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

# Wait for the worker to report that it is ready (it pings Cube at startup).
for _ in $(seq 1 60); do
  if grep -q "factory worker starting" "${WORKER_LOG}" 2>/dev/null; then
    break
  fi
  if ! kill -0 "${WORKER_PID}" 2>/dev/null; then
    echo "worker exited during startup:" >&2
    tail -40 "${WORKER_LOG}" >&2
    exit 1
  fi
  sleep 1
done
if ! grep -q "factory worker starting" "${WORKER_LOG}" 2>/dev/null; then
  echo "worker did not become ready in time:" >&2
  tail -40 "${WORKER_LOG}" >&2
  exit 1
fi
echo "   ready (pid ${WORKER_PID})"
echo

echo "== submitting task =="
set +e
./bin/factory run \
  --local-path "${FIXTURE_PATH}" \
  --rev "${FIXTURE_SHA}" \
  --task "${TASK}" \
  --agent "${AGENT}" \
  --verification "${VERIFICATION}" \
  --wait
RUN_EXIT=$?
set -e

echo
echo "== leakage check =="
set +e
./bin/factory sandboxes
SANDBOX_EXIT=$?
set -e
if [ "${SANDBOX_EXIT}" -ne 0 ]; then
  echo "FAIL: factory-owned sandboxes are still alive" >&2
fi

echo
if [ "${RUN_EXIT}" -eq 0 ] && [ "${SANDBOX_EXIT}" -eq 0 ]; then
  echo "PHASE 1 ACCEPTANCE TEST: PASSED"
  exit 0
fi
echo "PHASE 1 ACCEPTANCE TEST: FAILED (run exit ${RUN_EXIT}, sandbox exit ${SANDBOX_EXIT})" >&2
exit 1
