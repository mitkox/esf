# unreal-agent harness (async, no dependency)

`unreal-agent` is an async-first agent harness that fits the factory's durable
execution model. This document is the operator contract for running it under
ESF **without adding a Go module dependency**.

## Why async matters here

| unreal-agent property | Factory benefit |
| --- | --- |
| Session inbox (idempotent input IDs) | Safe Temporal retries; redelivered inputs do not double-apply |
| Append-only persisted session + forks | Crash recovery; review rework (`--change/--parent-run`) maps to a fork |
| Tool translator (sync, no I/O) → serializable `Operation` → async actor runtime | Long tools never block the LLM loop; bounded by `agent_timeout` + activity deadline + sandbox TTL |
| Context builder (in-memory, no I/O) | Deterministic model input; omissions recorded, not hidden |
| Versioned session/operation format | Evidence bundle stays readable across releases; unsupported versions fail loudly on resume |
| Provider abstraction (`openai/openrouter/fireworks/ollama`) | One harness serves many models via `pass_env`; the model id is recorded on the harness |

Agent success is never factory success: `agent_result` is recorded, but
`factory_result` is decided by the deterministic verification gates.

## Integration shape (the only supported one)

`type = "generic"`. ESF stages the standalone runner binary and invokes it
through a fixed `sh` wrapper. There is deliberately:

* no `require github.com/unreallabsai/unreal-agent` in `go.mod`,
* no caller-supplied executable (registry resolves by name only),
* no `base_url` wiring on the generic harness (gateway travels via `pass_env`).

```toml
[harnesses.unreal]
type        = "generic"
binary      = "/home/USER/dev/unreal-agent/bin/unreal-agent-runner"
executable  = "/bin/sh"
args        = ["-c", "export THINKING_LEVEL=\"${THINKING_LEVEL:-high}\"; python3 -c 'import os,sys,json; sys.stdout.write(json.dumps({\"prompt\": sys.stdin.read(), \"thinking_level\": os.environ[\"THINKING_LEVEL\"]}))' | exec /usr/local/bin/unreal -session-directory /tmp/unreal-sessions -log-directory /tmp/unreal-logs"]
model       = "<provider-model-id>"
timeout     = "30m"
packages    = ["python3"]
prompt_mode = "stdin_file"
pass_env    = ["UNREAL_HARNESS_LLM_PROVIDER", "UNREAL_HARNESS_LLM_BASE_URL", "UNREAL_HARNESS_LLM_MODEL", "THINKING_LEVEL"]
```

The gateway URL and credential travel via `pass_env` from the worker
environment; their values never enter config or evidence. The harness
`model` field records the model id; `--model` may override it per run.

Provisioning (automatic via `BuildHarnesses`): `binary` → `/usr/local/bin/unreal`,
`packages` installed, verified with `unreal --version`. The prompt arrives on
stdin as raw task text; the wrapper converts it to the runner's JSON request.
`THINKING_LEVEL` travels via environment so one harness serves models with
different limits.

## Operator runbook

Required environment (worker host):

```sh
export UNREAL_HARNESS_LLM_PROVIDER=openrouter
export UNREAL_HARNESS_LLM_BASE_URL=https://openrouter.ai/api/v1
export UNREAL_HARNESS_LLM_MODEL=<model-id supporting Responses API + tools>
export OPENROUTER_API_KEY=<non-empty>
```

The sandbox cannot dial host loopback: a `127.0.0.1`/`localhost` gateway passes
a host-side smoke test but fails inside the microVM. Use a reachable cloud
endpoint. Only the `pass_env`-listed names reach the sandbox; everything else
on the host is dropped.

Demo (ESF-owned script, overlay config so `factory.toml` is untouched):

```sh
./scripts/unreal-demo.sh                    # full run: worker + task + verify + leak check
DRY_RUN=1 ./scripts/unreal-demo.sh          # build + config + doctor only
SKIP_SMOKE=1 DRY_RUN=1 ./scripts/unreal-demo.sh  # offline validation, no quota
```

Compare harnesses offline + live:

```sh
./benchmarks/harness-compare.sh             # offline: unit + doctor + fixture timing
./benchmarks/harness-compare.sh --live      # also runs noop/conformance/unreal live
```

## Evidence mapping

* Agent stdout JSONL → `.factory/runs/<id>/agent/{stdout.log,stderr.log,result.json}`
* Runner sessions/logs (`/tmp/unreal-sessions`, `/tmp/unreal-logs` in-sandbox)
  are captured before sandbox destroy when present.
* Manifest `run.json`: `factory_result` vs `agent_result`, harness name/model,
  `unreal --version`, `THINKING_LEVEL`.

## Safety notes

* Prompt delivered by `stdin_file`: untrusted text never enters the command string.
* Egress inherits the deployment default (public internet) for the cloud LLM
  endpoint + apt provisioning. Restrict per-scope with `[egress.*]` when the
  model gateway is allowlisted.
* `factory sandboxes` must exit zero after every run; a leaked microVM fails
  the demo and the benchmark.
