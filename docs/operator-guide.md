# Operator Guide

Everything an operator needs to run, inspect, and extend the factory.

Before deployment, review [production readiness](production-readiness.md) for
validated behavior, deployment boundaries, and live acceptance checks.

---

## 1. What the factory discovers and configures

The factory **consumes** an existing CubeSandbox installation. It never installs,
reconfigures, resets, or removes Cube components, and it never creates, mutates,
or deletes templates.

### How Cube is discovered

| Setting | How to find it | This host |
| --- | --- | --- |
| API endpoint | `GET /health` on the Cube API port | `http://127.0.0.1:4000` |
| Template | `cubemastercli tpl list`, pick a `READY` row | `tpl-your-ready-template` |
| Proxy node | data-plane host for `Host: <port>-<id>.cube.app` | `192.0.2.10` |
| Proxy port | `ss -tlnp` on the proxy node | `80` |
| Version | `/usr/local/services/cubetoolbox/VERSION.txt` | `v0.7.1-amd64` |

```bash
# Confirm the endpoint first: the SDK's default is port 3000, which is NOT this
# deployment's port.
curl -s http://127.0.0.1:4000/health
cubemastercli tpl list
./bin/factory doctor
```

`factory doctor` validates configuration, proves the Cube API answers, proves the
configured template exists, confirms Temporal is reachable, and lists the
registered harnesses. Run it before anything else.

### Which template is used

The value in `cube.template_id`, overridable per run with `--sandbox-template`
(an operator-facing flag, not an API-client capability). The factory never
creates a template. If none suits, the production direction is a new, versioned
factory template (`factory-agent-base-v1`) — a deliberate deployment change, made
by the operator.

## 2. Starting Temporal

```bash
make temporal-up       # start and wait for health
make temporal-status   # containers + namespace
make temporal-down     # stop (the data volume is preserved)
make temporal-clean    # stop AND delete the data volume
```

| Endpoint | Value |
| --- | --- |
| gRPC | `127.0.0.1:7233` |
| UI | <http://127.0.0.1:8233> |
| Namespace | `default` |
| PostgreSQL | `127.0.0.1:5433` |

All ports bind to `127.0.0.1`. Temporal is never reachable off-host.

```bash
make temporal-hello    # proves gRPC + namespace + a workflow actually executing
```

## 3. Where factory state lives

| State | Location | Notes |
| --- | --- | --- |
| Workflow state | Temporal (Docker volume `factory-temporal-postgresql-data`) | Survives `make temporal-down` |
| **Artifacts / evidence** | `.factory/runs/<run-id>/` (configurable via `storage.data_dir`) | Survives sandbox destruction |
| Configuration | `factory.toml` (or `$FACTORY_CONFIG`) | Gitignored locally; `factory init` writes a template |
| Worker log | stdout, or `.factory/worker.log` when started by the demo script | |

## 4. Running a task

```bash
./bin/factory run \
  --repo https://github.com/your-org/your-repo \   # or --local-path /path/to/repo
  --rev <exact-revision> \
  --task "Change the greeting from hello to hello factory" \
  --agent opencode2 \
  --verification default
```

The CLI prints the run id, workflow id, harness, template, and — on completion —
the three outcomes plus the patch path:

```
FACTORY RESULT: SUCCEEDED
AGENT RESULT:   SUCCESS
VERIFICATION:   PASSED
CLEANUP:        PASSED

Patch:
.factory/runs/run-…/changes.patch
```

Useful flags:

| Flag | Purpose |
| --- | --- |
| `--local-path` | Use a repository that exists on the factory host (packed as a git bundle). Used by the acceptance fixture. |
| `--model` | Override the harness's default model. |
| `--sandbox-template` | Override the template for this run. |
| `--run-id` | Supply an explicit run id (makes submission idempotent). |
| `--wait=false` | Submit and return immediately. |
| `--agent-timeout` | Override the agent timeout. |
| `--scope` | Run under a tenancy scope; the scope's declared resources apply. |
| `--workspace` | Use a named pre-warmed workspace. |
| `--egress` | Run under a named network policy. |
| `--change` | Attach the run to a durable change (default: the run itself). |
| `--parent-run` | Record the run this one reworks. |
| `--review` | Pause for human review before finalizing. |

### Declarative resources and the review gate

A run may reference operator-declared resources instead of repeating settings:
`[workspaces]`, `[models]`, `[egress]`, `[budgets]` and `[scopes]` in
`factory.toml`. Resolution happens once, in validation, and the resolved set
plus its digest is recorded in `run.json`. Unknown names are rejected — a typo
fails loudly rather than running with no policy.

With `[review] enabled = true` (or `--review`), a run pauses after the gates
report:

```bash
./bin/factory status <run-id> --watch          # stream conditions until it pauses
./bin/factory review <run-id> --approve
./bin/factory review <run-id> --reject --note "use the formal greeting"
```

The sandbox is checkpointed while paused when the provider supports it (the
manifest records `suspend_result`), preview URLs are published when
`preview_ports` is set (`preview_result` records `SUCCESS`, `FAILED`, or
`SKIPPED`), and a timeout is recorded as `timeout` — the run then finishes
normally. A rejection does not rewrite the gate result: the factory result
stays `SUCCEEDED` if the gates passed, and the redacted review note becomes the
change's rework instruction while the change returns to `OPEN`.

### Changes (durable work items)

Every run belongs to a change. Without `--change`, the run is its own change.
With `--change`, rework activations accumulate lineage and spend:

```bash
./bin/factory get changes
./bin/factory describe change <change-id>
./bin/factory run --change <id> --parent-run <reviewed-run-id> ...   # rework
```

### Operator access to a live sandbox

`factory attach` and `factory preview` are **default closed**. Enable them with
`[sandbox] allow_attach = true` / `allow_preview = true`; every use is audited
in `audit/actions.jsonl`. Neither resumes a suspended sandbox implicitly.

```bash
./bin/factory attach <run-id> -- ls -la /workspace/repository
./bin/factory preview <run-id> --port 3000
```

### Result states

| State | Meaning |
| --- | --- |
| `SUCCEEDED` | Every mandatory gate passed, patch collection completed, and sandbox cleanup was verified |
| `AGENT_FAILED` | The agent exited non-zero or timed out |
| `VERIFICATION_FAILED` | The agent claimed success; a deterministic gate disagreed |
| `INFRASTRUCTURE_FAILED` | Infrastructure, patch collection, or cleanup failed |
| `INVALID_REQUEST` | Rejected by policy; no sandbox was created |
| `PAUSED` | Waiting on a human review gate; the run resumes or times out |
| `CANCELLED` | The workflow was cancelled |

## 5. Inspecting a run

```bash
./bin/factory status <run-id>          # manifest if finished, live query otherwise
./bin/factory status <run-id> --json
./bin/factory status <run-id> --watch  # stream condition transitions
./bin/factory describe run <run-id>    # manifest + conditions + inventory + audit
./bin/factory logs   <run-id>          # artifact listing + agent output tails
```

Artifact layout:

```
.factory/runs/<run-id>/
  task.json                 the request as accepted (+ task_hash)
  baseline.json             source, requested revision, resolved SHA, branch, status
  git-before.txt            what the agent started from
  changes.patch             THE DELIVERABLE
  git-after.txt             post-run sha and status
  run.json                  the manifest
  cleanup.json              destroy outcome, verification, remaining sandboxes
  agent/{prompt.txt,stdout.log,stderr.log,result.json}
  verification/{result.json,<gate>.stdout,<gate>.stderr}
```

Quick triage:

```bash
R=.factory/runs/<run-id>
jq '{factory_result,agent_result,verification_result,cleanup_result}' $R/run.json
jq '.steps[] | {id,exit_code,passed,error}' $R/verification/result.json
cat $R/changes.patch
```

## 6. Cancelling a run

```bash
temporal workflow cancel --address 127.0.0.1:7233 \
  --namespace default --workflow-id factory-run-<run-id>
```

Cancellation is durable: the workflow's cleanup runs from a **disconnected
context**, so the microVM is destroyed even though the workflow was cancelled.
This is covered by an integration test.

## 7. How cleanup works

1. The sandbox ID is recorded as soon as it is created.
2. Cleanup is registered **before** creation, so every exit path is covered:
   success, agent failure, verification failure, timeout, cancellation.
3. `DestroySandbox` is retried up to 8 times with backoff — destroy is
   idempotent, and a leaked microVM is far more expensive than a retry.
4. After destroying, the activity **independently verifies** the sandbox is gone
   by listing live sandboxes, rather than trusting the destroy call.
5. The outcome is written to `cleanup.json` and to `run.json`.
6. The manifest is written **after** cleanup, so it always contains the result.

### Detecting leaked sandboxes

```bash
./bin/factory sandboxes
```

The command exits non-zero when any sandbox carries `origin=factory`. Every
factory sandbox is tagged with `origin`, `run_id`, `harness`, and `creator`, so a
leak is attributable to a specific run. The `sandbox_leaks_total` metric counts
them.

## 8. Rotating credentials

No Cube credential is required on this deployment (authentication is disabled).
If it is enabled:

```bash
# .env (gitignored) or the deployment environment
CUBE_API_KEY=<new-value>
```

Then restart the worker. `CUBE_API_KEY` is never logged — `cube.Config.String()`
renders it as `set(redacted)` — and is never written to any artifact.

For an agent provider credential, the factory supports two mechanisms:

| Mechanism | Config | Notes |
| --- | --- | --- |
| Environment allowlist | `pass_env = ["MY_TOKEN"]` | The variable is forwarded to the agent process only if its name is listed |
| Provisioned provider file | `provider_files = { "<sandbox path>" = "<host path>" }` | Opt-in. The file is copied into the sandbox so the agent can authenticate. |

Both are operator configuration. Neither is reachable from a request. When a
provider file is configured, its contents are also registered with the redactor,
so they cannot be persisted into evidence.

> **Trade-off, stated plainly:** a provisioned provider file is readable by the
> agent inside the sandbox. The cleaner alternative — an egress proxy that
> injects the credential header so it never enters the sandbox — is Phase 2 work;
> this deployment's Cube egress proxy already supports credential injection.

To rotate a provisioned provider file, replace the host file and restart the
worker; the new contents are staged on the next run and re-registered with the
redactor.

## 9. Adding a new agent harness

Add a block to `factory.toml` and restart the worker.

```toml
[harnesses.codex]
type        = "generic"
executable  = "codex"
args        = ["exec", "{{model_args}}", "--full-auto"]
model_flag  = "--model"
model       = "gpt-5.6-luna"
timeout     = "45m"
packages    = ["git"]
prompt_mode = "stdin_file"
pass_env    = ["OPENAI_API_KEY"]
```

| Field | Meaning |
| --- | --- |
| `type` | `opencode` (built-in shape) or `generic` (anything else) |
| `executable` | Operator-owned program name — never caller-supplied |
| `args` | Fixed argument vector. `{{model_args}}` expands to the model flag + model, or disappears |
| `model_flag` | The agent's model-selection flag |
| `packages` | Extra OS packages for this harness |
| `prompt_mode` | `stdin_file` (default, no interpolation) or `argv` |
| `pass_env` | Host variables allowed through to the agent. Nothing else passes |
| `binary` | Host path to a standalone binary to push into the sandbox |
| `catalog_cache` | Host path to a model catalog, staged to make model resolution offline |
| `provider_files` | In-sandbox path → host path, staged for agent authentication |

Notes:

- `{{model_args}}` is required to appear **after** the subcommand for CLIs like
  `opencode2 run --model X`. A fixed insertion point cannot express both shapes.
- The factory installs `sandbox.base_packages` (default `git`,
  `ca-certificates`) in every sandbox regardless of harness, because repository
  preparation needs them.
- The harness is selected by **name** (`--agent codex`). A request cannot name an
  executable.

Verify with `./bin/factory doctor`, which lists registered harnesses.

For the async `unreal-agent` runner (standalone binary, no Go dependency, full
operator contract + evidence mapping), see [harness-unreal](harness-unreal.md)
and `scripts/unreal-demo.sh`.

## 10. Adding a verification profile

```toml
[verification.strict]
name = "strict"

[[verification.strict.steps]]
id          = "fmt"
argv        = ["gofmt", "-l", "."]
mandatory   = true
timeout     = "2m"

[[verification.strict.steps]]
id          = "build"
argv        = ["./build.sh"]
mandatory   = true
timeout     = "10m"

[[verification.strict.steps]]
id          = "unit-tests"
argv        = ["go", "test", "./..."]
mandatory   = true
timeout     = "20m"
dir         = "backend"      # relative to the repository root

[[verification.strict.steps]]
id          = "lint"
argv        = ["golangci-lint", "run"]
mandatory   = false          # advisory: recorded but does not gate
```

Rules:

- **Always a structured `argv`.** There is no shell-string form, by design.
- `mandatory` defaults to `true`. An opt-out gate is the surprising case.
- `dir` must be relative and must not contain `..`.
- The factory result is `PASSED` only when **every mandatory step exits zero**.
- Output is captured per gate and redacted before storage.

Run a profile with `--verification strict`.

## 10a. Sandbox environment setup

Some toolchains cannot be expressed as OS packages — a language runtime's
dependencies, for example. `sandbox.setup_script` is factory-owned shell code
that runs after `base_packages` and **before** the repository is cloned, so it
can prepare the image but cannot touch the checkout.

```toml
[sandbox]
base_packages = ["git", "ca-certificates", "python3-pip"]
setup_script = """
set -eu
export DEBIAN_FRONTEND=noninteractive
python3 -m pip install --break-system-packages -q setuptools pytest pydantic PyYAML jsonschema
echo sandbox-setup-ok
"""
```

It is OPERATOR configuration and must never contain task-derived text. Anything
that needs the checkout (such as `pip install -e .` for a src-layout Python
project) belongs in a verification gate, which runs after the clone.

## 10b. Verification that mutates the working tree

The deliverable patch is captured **immediately after the agent runs** and
before any gate executes. If a gate rewrites a tracked file — a build step
regenerating a lockfile, for example — the factory does not fold that into the
patch. It records the drift separately and warns:

```
WARNING: a verification gate modified the working tree.
         The deliverable patch is captured BEFORE verification, so that
         change is not in changes.patch.
         Evidence: <run-id>/verification/post-verification.patch
```

The manifest carries `verification_mutated_tree` and `post_verification_patch`.
If a gate routinely mutates the tree, make it side-effect free or move the
mutation into the build step — the factory will keep telling you.

## 11. Resource limits

Configured under `[limits]`:

| Setting | Default | Enforced |
| --- | --- | --- |
| `agent_timeout` | 30m | Agent command timeout and activity deadline |
| `verification_timeout` | 15m | Whole verification activity context, plus per-gate timeouts |
| `total_timeout` | 90m | Durable workflow timer for execution; cleanup runs afterwards |
| `sandbox_idle_timeout` | 60m | Cube idle timeout |

A runaway agent cannot consume unlimited resources: it is bounded by the agent
timeout, the activity timeout, and the sandbox idle TTL independently.

## 12. Troubleshooting

| Symptom | Likely cause | Action |
| --- | --- | --- |
| `ERR_PROXY_CONNECTION_FAILED` / connection refused on `:4000` | Wrong Cube port, or Cube not running | `curl http://127.0.0.1:4000/health`; check `systemctl status cube-sandbox-cube-api` |
| `template not found` | Template id changed or was deleted | `cubemastercli tpl list`; update `cube.template_id` |
| `template is required` | `cube.template_id` unset | Set `CUBE_TEMPLATE_ID` or the config file |
| `git: command not found` during `repo.prepare` | `sandbox.base_packages` was emptied | Restore `["git", "ca-certificates"]` |
| Agent exits 1 with `Model unavailable` | The agent's provider is not authenticated inside the sandbox | Provision a credential (`pass_env` or `provider_files`); see §8 |
| `INFRASTRUCTURE_FAILED` at `cube.create` | Cube API unreachable or the template is not READY | `factory doctor` |
| `factory sandboxes` exits non-zero | A sandbox leaked | Inspect the `run_id` metadata and the run's `cleanup.json` |
| Worker idle, run never starts | Task queue mismatch, or no worker running | Compare `temporal.task_queue` in config with the worker log; `factory worker` |

## 13. Development commands

```bash
make build              # factory + discovery binaries into ./bin
make test               # unit tests (no Cube, no Temporal)
make lint               # gofmt, go vet, shellcheck
make verify             # lint + test + cube-smoke

make temporal-up        # Temporal + PostgreSQL + UI, loopback only
make temporal-status
make temporal-hello
make temporal-down

make cube-smoke         # prove the host can use the existing CubeSandbox
make cube-netprobe      # discover the deployment's egress behaviour
make cube-agent-spike   # prove a coding agent runs inside a microVM
make cube-sandboxes     # leak check

make integration-test   # Cube + Temporal integration suites
make factory-doctor
make factory-worker
make factory-run-demo   # the Phase 1 acceptance test
```
