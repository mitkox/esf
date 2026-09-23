#!/usr/bin/env bash
# scripts/unreal-demo.sh — ESF acceptance demo with unreal-agent (generic harness).
#
# One task in → Temporal workflow → isolated CubeSandbox microVM →
# unreal-agent-runner → deterministic verification → verified patch out.
#
# No Go dependency: the runner is a standalone binary staged into the sandbox
# via [harnesses.unreal] binary. The committed factory.toml is untouched; this
# script writes an overlay config under .factory/.
#
# Required environment (export before running):
#   UNREAL_HARNESS_LLM_PROVIDER  e.g. openrouter
#   UNREAL_HARNESS_LLM_BASE_URL  e.g. https://openrouter.ai/api/v1
#   UNREAL_HARNESS_LLM_MODEL     model id supporting Responses API + tools
#   <key var>                    e.g. OPENROUTER_API_KEY (non-empty)
#
# Optional:
#   UNREAL_RUNNER   host path to unreal-agent-runner (default: ../unreal-agent/bin/unreal-agent-runner)
#   MODEL_API_KEY_ENV  override credential env name
#   THINKING_LEVEL     default: high
#   FACTORY_CONFIG     base operator config (default: ./factory.toml)
#
# Usage:
#   ./scripts/unreal-demo.sh
#   DRY_RUN=1 ./scripts/unreal-demo.sh
#   SKIP_SMOKE=1 DRY_RUN=1 ./scripts/unreal-demo.sh
set -euo pipefail

ESF_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNNER="${UNREAL_RUNNER:-$ESF_ROOT/../unreal-agent/bin/unreal-agent-runner}"
BASE_CONFIG="${FACTORY_CONFIG:-$ESF_ROOT/factory.toml}"
BOOK_TASK='Change the greeting from "hello" to "hello factory". Update the tests appropriately so they pass.'

PROVIDER="${UNREAL_HARNESS_LLM_PROVIDER:-}"
BASE_URL="${UNREAL_HARNESS_LLM_BASE_URL:-}"
MODEL_ID="${UNREAL_HARNESS_LLM_MODEL:-}"
THINKING_LEVEL="${THINKING_LEVEL:-high}"
DRY_RUN="${DRY_RUN:-0}"
SKIP_SMOKE="${SKIP_SMOKE:-0}"
RUN_ID="unreal-demo-$(date +%Y%m%d-%H%M%S)"
DEMO_CONFIG="$ESF_ROOT/.factory/demo-unreal.toml"
WORKER_LOG="$ESF_ROOT/.factory/worker-unreal-demo.log"

say() { printf '\n== %s ==\n' "$*"; }
die() { printf 'unreal-demo.sh: ERROR: %s\n' "$*" >&2; exit 1; }

default_key_env() {
  case "$PROVIDER" in
    openrouter) echo "OPENROUTER_API_KEY" ;;
    openai) echo "OPENAI_API_KEY" ;;
    fireworks) echo "FIREWORKS_API_KEY" ;;
    *) echo "" ;;
  esac
}
KEY_ENV="${MODEL_API_KEY_ENV:-$(default_key_env)}"

say "Prerequisites"
[ -f "$BASE_CONFIG" ] || die "operator config not found: $BASE_CONFIG"
[ -x "$RUNNER" ] || die "runner not built: $RUNNER (run make -C $(dirname "$RUNNER")/.. build or set UNREAL_RUNNER)"
[ -n "$PROVIDER" ] || die "UNREAL_HARNESS_LLM_PROVIDER is required (e.g. openrouter)"
[ -n "$BASE_URL" ] || die "UNREAL_HARNESS_LLM_BASE_URL is required"
[ -n "$MODEL_ID" ] || die "UNREAL_HARNESS_LLM_MODEL is required"
[ -n "$KEY_ENV" ] || die "MODEL_API_KEY_ENV is required (no default for provider $PROVIDER)"
[ -n "${!KEY_ENV:-}" ] || die "$KEY_ENV is empty (export it before running)"
command -v curl >/dev/null || die "curl not on PATH"
command -v python3 >/dev/null || die "python3 not on PATH"
case "$BASE_URL" in
  *127.0.0.1*|*localhost*)
    printf 'WARNING: BASE_URL=%s is host loopback; sandbox cannot dial it.\n' "$BASE_URL" >&2
    ;;
esac
printf 'provider: %s model: %s key: %s (set)\n' "$PROVIDER" "$MODEL_ID" "$KEY_ENV"

say "Model smoke test (host side)"
if [ "$SKIP_SMOKE" = "1" ]; then
  printf 'skipped (SKIP_SMOKE=1)\n'
else
  mkdir -p /tmp/opencode
  HTTP="$(curl -s -m 20 -o /tmp/opencode/model-smoke.json -w '%{http_code}' \
    -H "Authorization: Bearer ${!KEY_ENV}" "$BASE_URL/models")" \
    || die "model endpoint unreachable: $BASE_URL"
  [ "$HTTP" = "200" ] || die "$BASE_URL/models -> HTTP $HTTP (check key + URL)"
  if grep -q "\"$MODEL_ID\"" /tmp/opencode/model-smoke.json; then
    printf 'model %s listed at %s\n' "$MODEL_ID" "$BASE_URL"
  else
    printf 'WARNING: model %s not in catalog listing; continuing\n' "$MODEL_ID"
  fi
fi

say "Build"
cd "$ESF_ROOT"
make --no-print-directory build >/dev/null || die "esf build failed"
"$RUNNER" --version
printf 'built: factory + unreal-agent-runner\n'

say "Fixture"
FIXTURE_OUTPUT="$("$ESF_ROOT/scripts/make-e2e-repo.sh")"
FIXTURE_PATH="$(echo "$FIXTURE_OUTPUT" | sed -n 's/^fixture_path=//p')"
FIXTURE_SHA="$(echo "$FIXTURE_OUTPUT" | sed -n 's/^fixture_sha=//p')"
[ -n "$FIXTURE_PATH" ] && [ -n "$FIXTURE_SHA" ] || die "fixture setup failed"
printf 'path: %s\nsha:  %s\n' "$FIXTURE_PATH" "$FIXTURE_SHA"

say "Demo operator config (overlay)"
mkdir -p "$ESF_ROOT/.factory"
cp "$BASE_CONFIG" "$DEMO_CONFIG"
cat >>"$DEMO_CONFIG" <<EOF

# --- DEMO OVERLAY (appended by scripts/unreal-demo.sh; not part of factory.toml)
[harnesses.unreal]
type = "generic"
binary = "$RUNNER"
executable = "/bin/sh"
args = ["-c", "export THINKING_LEVEL=\"\${THINKING_LEVEL:-high}\"; python3 -c 'import os,sys,json; sys.stdout.write(json.dumps({\"prompt\": sys.stdin.read(), \"thinking_level\": os.environ[\"THINKING_LEVEL\"]}))' | exec /usr/local/bin/unreal -session-directory /tmp/unreal-sessions -log-directory /tmp/unreal-logs"]
model = "$MODEL_ID"
timeout = "30m"
packages = ["python3"]
pass_env = ["UNREAL_HARNESS_LLM_PROVIDER", "UNREAL_HARNESS_LLM_BASE_URL", "UNREAL_HARNESS_LLM_MODEL", "THINKING_LEVEL"]
prompt_mode = "stdin_file"
EOF
printf 'overlay written: %s\n' "$DEMO_CONFIG"

export UNREAL_HARNESS_LLM_PROVIDER="$PROVIDER"
export UNREAL_HARNESS_LLM_BASE_URL="$BASE_URL"
export UNREAL_HARNESS_LLM_MODEL="$MODEL_ID"
export THINKING_LEVEL
# shellcheck disable=SC2163
export "$KEY_ENV" 2>/dev/null || true

say "Doctor (validates config + harness registry)"
./bin/factory --config "$DEMO_CONFIG" doctor || die "doctor failed"

if [ "$DRY_RUN" = "1" ]; then
  printf 'DRY_RUN=1: stopping before worker/run. Config is valid.\n'
  exit 0
fi

say "Worker"
./bin/factory --config "$DEMO_CONFIG" worker >"$WORKER_LOG" 2>&1 &
WORKER_PID=$!
cleanup() {
  if kill -0 "$WORKER_PID" 2>/dev/null; then
    kill "$WORKER_PID" 2>/dev/null || true
    wait "$WORKER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT
for _ in $(seq 1 60); do
  if grep -q "factory worker starting" "$WORKER_LOG" 2>/dev/null; then
    break
  fi
  if ! kill -0 "$WORKER_PID" 2>/dev/null; then
    echo "worker exited during startup:" >&2
    tail -40 "$WORKER_LOG" >&2
    exit 1
  fi
  sleep 1
done
grep -q "factory worker starting" "$WORKER_LOG" 2>/dev/null \
  || { echo "worker did not become ready:" >&2; tail -40 "$WORKER_LOG" >&2; exit 1; }
printf 'ready (pid %s)\n' "$WORKER_PID"

say "Run ($RUN_ID)"
TASK_FILE="$(mktemp)"
printf '%s' "$BOOK_TASK" >"$TASK_FILE"
set +e
./bin/factory --config "$DEMO_CONFIG" run \
  --run-id "$RUN_ID" \
  --local-path "$FIXTURE_PATH" \
  --rev "$FIXTURE_SHA" \
  --task-file "$TASK_FILE" \
  --agent unreal \
  --verification default \
  --agent-timeout 20m \
  --wait
RUN_EXIT=$?
set -e
rm -f "$TASK_FILE"

say "Inspect"
./bin/factory --config "$DEMO_CONFIG" status "$RUN_ID" || true
RUN_DIR="$ESF_ROOT/.factory/runs/$RUN_ID"
if [ -d "$RUN_DIR" ]; then
  find "$RUN_DIR" -maxdepth 2 | sort
  python3 -c "
import json
m = json.load(open('$RUN_DIR/run.json'))
print('factory_result:', m.get('factory_result'))
print('agent_result:  ', m.get('agent_result'))
print('harness:       ', m.get('harness'))
print('model:         ', m.get('model'))
" 2>/dev/null || true
  if [ -f "$RUN_DIR/changes.patch" ]; then
    head -n 30 "$RUN_DIR/changes.patch"
  else
    printf '(no changes.patch — see agent/ + verification/ evidence)\n'
  fi
else
  printf '(no run directory yet — inspect worker log: %s)\n' "$WORKER_LOG"
fi

say "Leak check"
set +e
./bin/factory --config "$DEMO_CONFIG" sandboxes
SANDBOX_EXIT=$?
set -e

say "Verdict"
if [ "$RUN_EXIT" -eq 0 ] && [ "$SANDBOX_EXIT" -eq 0 ]; then
  printf 'ESF + UNREAL-AGENT DEMO: PASSED\n'
  exit 0
fi
printf 'ESF + UNREAL-AGENT DEMO: FAILED (run exit %s, sandbox exit %s)\n' "$RUN_EXIT" "$SANDBOX_EXIT" >&2
exit 1
