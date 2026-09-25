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
BENCH_TMP="$(mktemp -d)"
WPID=""
cleanup() {
  if [ -n "$WPID" ] && kill -0 "$WPID" 2>/dev/null; then
    kill "$WPID" 2>/dev/null || true
    wait "$WPID" 2>/dev/null || true
  fi
  rm -rf -- "$BENCH_TMP"
}
trap cleanup EXIT

now_ns() { date +%s%N; }
ms() { echo $(( ($2 - $1) / 1000000 )); }

report_row() { printf '  %-34s %8s ms  %s\n' "$1" "$2" "$3"; }

echo "== harness-compare (live=$LIVE) =="
echo "root: $ROOT"
command -v go >/dev/null || { echo "go not on PATH" >&2; exit 1; }

t0=$(now_ns); go test ./internal/agentharness/... -count=1 >"$BENCH_TMP/agentharness.log" 2>&1; t1=$(now_ns)
UNIT_HARNESS_MS=$(ms "$t0" "$t1")
tail -2 "$BENCH_TMP/agentharness.log" | head -1

t0=$(now_ns); go test ./internal/factory/... -count=1 >"$BENCH_TMP/factory.log" 2>&1; t1=$(now_ns)
UNIT_FACTORY_MS=$(ms "$t0" "$t1")
tail -2 "$BENCH_TMP/factory.log" | head -1

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
  SKIP_SMOKE=1 DRY_RUN=1 ./scripts/unreal-demo.sh >"$BENCH_TMP/unreal-dry.log" 2>&1
  t1=$(now_ns)
  report_row "unreal DRY_RUN doctor" "$(ms "$t0" "$t1")" "SKIP_SMOKE=1 DRY_RUN=1 scripts/unreal-demo.sh"
  grep -E "doctor|valid|overlay" "$BENCH_TMP/unreal-dry.log" | tail -3 || true
else
  echo "  skipped: UNREAL_HARNESS_LLM_* not set (offline validation needs provider/base_url/model)"
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
  local expected_factory="$3"
  local expected_agent="$4"
  local run_id
  run_id="bench-$(date +%Y%m%d-%H%M%S)-$agent"
  local fixture fixture_path fixture_sha code verdict
  fixture="$(./scripts/make-e2e-repo.sh)"
  fixture_path="$(echo "$fixture" | sed -n 's/^fixture_path=//p')"
  fixture_sha="$(echo "$fixture" | sed -n 's/^fixture_sha=//p')"
  t0=$(now_ns)
  set +e
  ./bin/factory run --run-id "$run_id" --local-path "$fixture_path" --rev "$fixture_sha" \
    --task 'Change the greeting from "hello" to "hello factory". Update the tests appropriately so they pass.' \
    --agent "$agent" --verification "$profile" --wait >"$BENCH_TMP/live-$agent.log" 2>&1
  code=$?
  set -e
  t1=$(now_ns)
  verdict="$(python3 -c 'import json,sys; m=json.load(open(sys.argv[1])); print(m.get("factory_result"), m.get("agent_result"))' ".factory/runs/$run_id/run.json" 2>/dev/null || echo "no-manifest")"
  report_row "live:$agent" "$(ms "$t0" "$t1")" "exit=$code $verdict"
  if [ "$verdict" != "$expected_factory $expected_agent" ]; then
    echo "unexpected $agent verdict: $verdict" >&2
    return 1
  fi
  if { [ "$expected_factory" = "SUCCEEDED" ] && [ "$code" -ne 0 ]; } || \
     { [ "$expected_factory" != "SUCCEEDED" ] && [ "$code" -eq 0 ]; }; then
    echo "unexpected $agent CLI exit $code for $expected_factory" >&2
    return 1
  fi
}

./bin/factory worker >.factory/worker-bench.log 2>&1 &
WPID=$!
for _ in $(seq 1 60); do
  grep -q "factory worker starting" .factory/worker-bench.log 2>/dev/null && break
  kill -0 "$WPID" 2>/dev/null || { tail -40 .factory/worker-bench.log >&2; exit 1; }
  sleep 1
done
grep -q "factory worker starting" .factory/worker-bench.log 2>/dev/null \
  || { echo "worker did not become ready" >&2; tail -40 .factory/worker-bench.log >&2; exit 1; }

live_one noop default VERIFICATION_FAILED SUCCESS
live_one conformance default SUCCEEDED SUCCESS
if ! ./bin/factory sandboxes; then
  echo "LEAK CHECK: FAILED" >&2
  exit 1
fi
echo "LEAK CHECK: PASSED"

if [ -n "${UNREAL_HARNESS_LLM_PROVIDER:-}" ]; then
  kill "$WPID" 2>/dev/null || true
  wait "$WPID" 2>/dev/null || true
  WPID=""
  ./scripts/unreal-demo.sh
fi
echo "LIVE BENCHMARK: DONE"
