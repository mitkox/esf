#!/usr/bin/env bash
# scripts/unreal-demo.sh — ESF acceptance demo with the unreal-agent adapter.
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
WORKER_PID=""
TASK_FILE=""
SMOKE_FILE=""

say() { printf '\n== %s ==\n' "$*"; }
die() { printf 'unreal-demo.sh: ERROR: %s\n' "$*" >&2; exit 1; }
cleanup() {
  [ -z "$TASK_FILE" ] || rm -f -- "$TASK_FILE"
  [ -z "$SMOKE_FILE" ] || rm -f -- "$SMOKE_FILE"
  if [ -n "$WORKER_PID" ] && kill -0 "$WORKER_PID" 2>/dev/null; then
    kill "$WORKER_PID" 2>/dev/null || true
    wait "$WORKER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

toml_string() {
  python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$1"
}

default_key_env() {
  case "$PROVIDER" in
    openrouter) echo "OPENROUTER_API_KEY" ;;
    openai) echo "OPENAI_API_KEY" ;;
    fireworks) echo "FIREWORKS_API_KEY" ;;
    *) echo "" ;;
  esac
}
KEY_ENV="${MODEL_API_KEY_ENV:-$(default_key_env)}"
CREDENTIAL_MODE="environment"
if [ "$PROVIDER" != "ollama" ]; then
  CREDENTIAL_MODE="cube_egress"
fi

say "Prerequisites"
[ -f "$BASE_CONFIG" ] || die "operator config not found: $BASE_CONFIG"
[ -x "$RUNNER" ] || die "runner not built: $RUNNER (run make -C $(dirname "$RUNNER")/.. build or set UNREAL_RUNNER)"
[ -n "$PROVIDER" ] || die "UNREAL_HARNESS_LLM_PROVIDER is required (e.g. openrouter)"
[ -n "$BASE_URL" ] || die "UNREAL_HARNESS_LLM_BASE_URL is required"
[ -n "$MODEL_ID" ] || die "UNREAL_HARNESS_LLM_MODEL is required"
command -v curl >/dev/null || die "curl not on PATH"
command -v python3 >/dev/null || die "python3 not on PATH"
case "$PROVIDER" in
  openai|openrouter|fireworks|ollama) ;;
  *) die "unsupported provider: $PROVIDER" ;;
esac
case "$THINKING_LEVEL" in
  low|medium|high|xhigh|max) ;;
  *) die "THINKING_LEVEL must be low, medium, high, xhigh, or max" ;;
esac
if [ "$PROVIDER" != "ollama" ]; then
  [ -n "$KEY_ENV" ] || die "MODEL_API_KEY_ENV is required (no default for provider $PROVIDER)"
  [[ "$KEY_ENV" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || die "invalid credential environment name: $KEY_ENV"
  if [ "$SKIP_SMOKE" != "1" ] || [ "$DRY_RUN" != "1" ]; then
    [ -n "${!KEY_ENV:-}" ] || die "$KEY_ENV is empty (export it before running)"
  fi
fi
case "$BASE_URL" in
  *127.0.0.1*|*localhost*)
    printf 'WARNING: BASE_URL=%s is host loopback; sandbox cannot dial it.\n' "$BASE_URL" >&2
    ;;
esac
printf 'provider: %s model: %s key env: %s\n' "$PROVIDER" "$MODEL_ID" "${KEY_ENV:-none}"

say "Model smoke test (host side)"
if [ "$SKIP_SMOKE" = "1" ]; then
  printf 'skipped (SKIP_SMOKE=1)\n'
else
  SMOKE_FILE="$(mktemp)"
  CURL_HEADERS=()
  if [ -n "$KEY_ENV" ]; then
    CURL_HEADERS=(-H "Authorization: Bearer ${!KEY_ENV}")
  fi
  HTTP="$(curl --silent --show-error --max-time 20 --output "$SMOKE_FILE" --write-out '%{http_code}' \
    "${CURL_HEADERS[@]}" -- "$BASE_URL/models")" \
    || die "model endpoint unreachable: $BASE_URL"
  [ "$HTTP" = "200" ] || die "$BASE_URL/models -> HTTP $HTTP (check key + URL)"
  if python3 -c 'import json,sys; needle=sys.argv[2]; data=json.load(open(sys.argv[1])); found=lambda x: x == needle if isinstance(x,str) else any(found(v) for v in x.values()) if isinstance(x,dict) else any(found(v) for v in x) if isinstance(x,list) else False; raise SystemExit(0 if found(data) else 1)' "$SMOKE_FILE" "$MODEL_ID"; then
    printf 'model %s listed at %s\n' "$MODEL_ID" "$BASE_URL"
  else
    printf 'WARNING: model %s not in catalog listing; continuing\n' "$MODEL_ID"
  fi
  rm -f -- "$SMOKE_FILE"
  SMOKE_FILE=""
fi

say "Build"
cd "$ESF_ROOT"
make --no-print-directory build >/dev/null || die "esf build failed"
"$RUNNER" -h >/dev/null 2>&1 || die "unreal-agent-runner does not satisfy the CLI contract"
RUNNER_SHA256="$(sha256sum "$RUNNER" | awk '{print $1}')"
printf 'built: factory + unreal-agent-runner (%s)\n' "$RUNNER_SHA256"

say "Fixture"
FIXTURE_OUTPUT="$("$ESF_ROOT/scripts/make-e2e-repo.sh")"
FIXTURE_PATH="$(echo "$FIXTURE_OUTPUT" | sed -n 's/^fixture_path=//p')"
FIXTURE_SHA="$(echo "$FIXTURE_OUTPUT" | sed -n 's/^fixture_sha=//p')"
[ -n "$FIXTURE_PATH" ] && [ -n "$FIXTURE_SHA" ] || die "fixture setup failed"
printf 'path: %s\nsha:  %s\n' "$FIXTURE_PATH" "$FIXTURE_SHA"

say "Demo operator config (overlay)"
mkdir -p "$ESF_ROOT/.factory"
if grep -Eq '^\[harnesses\.unreal\][[:space:]]*$' "$BASE_CONFIG"; then
  die "base config already defines [harnesses.unreal]; use it directly or remove it before running the overlay demo"
fi
cp "$BASE_CONFIG" "$DEMO_CONFIG"
chmod 0600 "$DEMO_CONFIG"
RUNNER_TOML="$(toml_string "$RUNNER")"
RUNNER_SHA256_TOML="$(toml_string "$RUNNER_SHA256")"
PROVIDER_TOML="$(toml_string "$PROVIDER")"
BASE_URL_TOML="$(toml_string "$BASE_URL")"
MODEL_TOML="$(toml_string "$MODEL_ID")"
KEY_ENV_TOML="$(toml_string "$KEY_ENV")"
THINKING_TOML="$(toml_string "$THINKING_LEVEL")"
CREDENTIAL_MODE_TOML="$(toml_string "$CREDENTIAL_MODE")"
cat >>"$DEMO_CONFIG" <<EOF

# --- DEMO OVERLAY (appended by scripts/unreal-demo.sh; not part of factory.toml)
[harnesses.unreal]
type = "unreal"
binary = $RUNNER_TOML
binary_sha256 = $RUNNER_SHA256_TOML
provider = $PROVIDER_TOML
base_url = $BASE_URL_TOML
model = $MODEL_TOML
api_key_env = $KEY_ENV_TOML
credential_mode = $CREDENTIAL_MODE_TOML
thinking_level = $THINKING_TOML
timeout = "30m"
EOF
printf 'overlay written: %s\n' "$DEMO_CONFIG"

if [ -n "$KEY_ENV" ] && [ -n "${!KEY_ENV:-}" ]; then
  export "$KEY_ENV"
fi

say "Doctor (validates config + harness registry)"
DOCTOR_ARGS=()
if [ "$DRY_RUN" = "1" ] && [ "$SKIP_SMOKE" = "1" ]; then
  DOCTOR_ARGS=(--offline)
fi
./bin/factory --config "$DEMO_CONFIG" doctor "${DOCTOR_ARGS[@]}" || die "doctor failed"

if [ "$DRY_RUN" = "1" ]; then
  printf 'DRY_RUN=1: stopping before worker/run. Config is valid.\n'
  exit 0
fi

say "Worker"
./bin/factory --config "$DEMO_CONFIG" worker >"$WORKER_LOG" 2>&1 &
WORKER_PID=$!
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
rm -f -- "$TASK_FILE"
TASK_FILE=""

say "Inspect"
./bin/factory --config "$DEMO_CONFIG" status "$RUN_ID" || true
RUN_DIR="$ESF_ROOT/.factory/runs/$RUN_ID"
if [ -d "$RUN_DIR" ]; then
  find "$RUN_DIR" -maxdepth 2 | sort
  python3 -c '
import json
m = json.load(open(__import__("sys").argv[1]))
print("factory_result:", m.get("factory_result"))
print("agent_result:  ", m.get("agent_result"))
print("harness:       ", m.get("agent_harness"))
print("model:         ", m.get("model"))
' "$RUN_DIR/run.json" 2>/dev/null || true
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
