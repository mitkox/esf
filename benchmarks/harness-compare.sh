#!/usr/bin/env bash
# Benchmark: harness comparison (offline by default, live with --live)
#
# Offline (no Cube, no Temporal, no LLM quota):
#   unit tests for agentharness + factory config
#   factory build timing
#   fixture materialisation timing
#   unreal overlay config validation (needs UNREAL_* env + runner binary,
#     but with SKIP_SMOKE=1 DRY_RUN=1 it burns no quota and starts no worker)
#
# Live (needs Cube + Temporal + credentials):
#   ./benchmarks/harness-compare.sh --live
#   runs noop + conformance through the e2e fixture and, when UNREAL_* is set,
#   the unreal harness via scripts/unreal-demo.sh
#
# Results are printed as a timing table; live runs also report
# factory/agent/verification outcomes from each run manifest.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
LIVE=0
[ "${1:-}" = "--live" ] && LIVE=1

now_ns() { date +%s%N; }
ms() { echo $(( ($2 - $1) / 1000000 )); }

report_row() { printf '  %-34s %8s ms  %s\n' "$1" "$2" "$3"; }

echo "== harness-compare (live=$LIVE) =="
echo "root: $ROOT"
command -v go >/dev/null || { echo "go not on PATH" >&2; exit 1; }

t0=$(now_ns); go test ./internal/agentharness/... -count=1 >/tmp/harness-compare-agentharness.log 2>&1; t1=$(now_ns)
UNIT_HARNESS_MS=$(ms "$t0" "$t1")
tail -2 /tmp/harness-compare-agentharness.log | head -1

t0=$(now_ns); go test ./internal/factory/... -count=1 >/tmp/harness-compare-factory.log 2>&1; t1=$(now_ns)
UNIT_FACTORY_MS=$(ms "$t0" "$t1")
tail -2 /tmp/harness-compare-factory.log | head -1

t0=$(now_ns); make --no-print-directory build >/dev/null; t1=$(now_ns)
BUILD_MS=$(ms "$t0" "$t1")

t0=$(now_ns); FIX_OUT="$(./scripts/make-e2e-repo.sh)"; t1=$(now_ns)
FIXTURE_MS=$(ms "$t0" "$t1")
echo "$FIX_OUT"

echo
echo "-- offline timings --"
report_row "agentharness unit tests" "$UNIT_HARNESS_MS" "go test ./internal/agentharness/..."
report_row "factory unit tests" "$UNIT_FACTORY_MS" "go test ./internal/factory/..."
report_row "factory build" "$BUILD_MS" "make build"
report_row "e2e fixture" "$FIXTURE_MS" "scripts/make-e2e-repo.sh"

echo
echo "-- unreal overlay validation --"
if [ -n "${UNREAL_HARNESS_LLM_PROVIDER:-}" ] && [ -n "${UNREAL_HARNESS_LLM_BASE_URL:-}" ] && [ -n "${UNREAL_HARNESS_LLM_MODEL:-}" ]; then
  t0=$(now_ns)
  SKIP_SMOKE=1 DRY_RUN=1 ./scripts/unreal-demo.sh >/tmp/harness-compare-unreal-dry.log 2>&1
  t1=$(now_ns)
  report_row "unreal DRY_RUN doctor" "$(ms "$t0" "$t1")" "SKIP_SMOKE=1 DRY_RUN=1 scripts/unreal-demo.sh"
  grep -E "doctor|valid|overlay" /tmp/harness-compare-unreal-dry.log | tail -3 || true
else
  echo "  skipped: UNREAL_HARNESS_LLM_* not set (offline validation needs provider/base_url/model + key)"
fi

if [ "$LIVE" = "0" ]; then
  echo
  echo "OFFLINE BENCHMARK: DONE (use --live for Cube+Temporal runs)"
  exit 0
fi

echo
echo "-- live runs (Cube + Temporal required) --"
./bin/factory doctor || { echo "doctor failed; aborting live runs" >&2; exit 1; }

live_one() {
  local agent="$1"
  local profile="${2:-default}"
  local run_id
  run_id="bench-$(date +%Y%m%d-%H%M%S)-$agent"
  FIX2="$(./scripts/make-e2e-repo.sh)"
  FP="$(echo "$FIX2" | sed -n 's/^fixture_path=//p')"
  FS="$(echo "$FIX2" | sed -n 's/^fixture_sha=//p')"
  t0=$(now_ns)
  set +e
  ./bin/factory run --run-id "$run_id" --local-path "$FP" --rev "$FS" \
    --task 'Change the greeting from "hello" to "hello factory". Update the tests appropriately so they pass.' \
    --agent "$agent" --verification "$profile" --wait >/tmp/harness-compare-live-"$agent".log 2>&1
  code=$?
  set -e
  t1=$(now_ns)
  verdict="$(python3 -c "import json; m=json.load(open('.factory/runs/$run_id/run.json')); print(m.get('factory_result'), m.get('agent_result'))" 2>/dev/null || echo "no-manifest")"
  report_row "live:$agent" "$(ms "$t0" "$t1")" "exit=$code $verdict"
}

./bin/factory worker >.factory/worker-bench.log 2>&1 &
WPID=$!
trap 'kill $WPID 2>/dev/null || true' EXIT
for _ in $(seq 1 60); do grep -q "factory worker starting" .factory/worker-bench.log 2>/dev/null && break; sleep 1; done

live_one noop
live_one conformance || true
if [ -n "${UNREAL_HARNESS_LLM_PROVIDER:-}" ]; then
  # unreal live run uses scripts/unreal-demo.sh full mode (own overlay config)
  echo "  (unreal live run: use scripts/unreal-demo.sh full mode for the harness run)"
fi
./bin/factory sandboxes && echo "LEAK CHECK: PASSED" || echo "LEAK CHECK: FAILED"
echo "LIVE BENCHMARK: DONE"
