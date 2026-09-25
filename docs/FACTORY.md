# Factory architecture

This repository is an **evolutionary fork of [Machinist](https://github.com/owainlewis/machinist)**
that adds a self-hosted agentic software factory on top of it, using
[CubeSandbox](https://github.com/TencentCloud/CubeSandbox) microVMs as the
isolated execution fabric.

Machinist's own single-process execution model, control plane and web UI are
**unchanged**. The factory is a parallel control plane in the same module.

## The vertical slice

```
        Task
          ↓
   Factory Control Plane            cmd/factory
          ↓
   Temporal Durable Workflow        internal/factory
          ↓
      CubeSandbox                   internal/sandbox/cube
          ↓
   OpenCode / any coding agent      internal/agentharness
          ↓
   Deterministic build + test       internal/verification
          ↓
   Patch + evidence + artifacts     internal/artifacts
          ↓
     Verified result
```

**One task in. One isolated Cube microVM. One agent. One verified patch out.**

## Why the boundaries are where they are

| Principle | How it is enforced |
| --- | --- |
| Agents are replaceable | `internal/agentharness.Harness`; a request names a harness, never an executable |
| Sandboxes are disposable | One microVM per run, destroyed on every exit path including cancellation |
| Workflow state is durable | Temporal; workflow code performs no I/O |
| Evidence is durable | `internal/artifacts` on the factory host — never only inside a microVM |
| Deterministic tools decide | `internal/verification` reads exit codes only; the model never declares success |
| Security from day one | Structured argv, allowlists, redaction, a microVM the agent cannot escape |
| Every run is reproducible | Exact revision, baseline SHA, task hash, harness, template and gate results recorded |

The distinction that matters most:

```
agent_result        = SUCCESS
verification_result = FAILED
factory_result      = VERIFICATION_FAILED
```

An agent saying it succeeded is never enough.

## Prerequisites

| Requirement | Notes |
| --- | --- |
| **An existing, running CubeSandbox** | Discovered, never installed or modified by this project |
| Go | `go 1.26.6` (auto-downloaded by `GOTOOLCHAIN=auto`) |
| Docker + Compose | For Temporal and PostgreSQL only |
| Git | For repository preparation inside sandboxes |
| A coding agent | `opencode2` v2.0.9 is pinned; **a model credential is required** (see below) |

## Setup

```bash
cp .env.example .env          # fill in placeholders
./bin/factory init            # or: make build && ./bin/factory init
make build
make factory-doctor           # validates config, Cube and Temporal
```

## Commands

```bash
# ── CubeSandbox ─────────────────────────────────────────────────────────────
make cube-smoke               # prove create/exec/destroy against the live install
make cube-netprobe            # discover the effective egress policy
make cube-agent-spike         # prove a coding agent runs inside a microVM
make cube-sandboxes           # leak check (exits non-zero if a sandbox leaked)

# ── Temporal ────────────────────────────────────────────────────────────────
make temporal-up              # Temporal + PostgreSQL + UI (loopback only)
make temporal-status
make temporal-hello           # prove a workflow actually executes
make temporal-down

# ── Build and test ──────────────────────────────────────────────────────────
make build
make test                     # unit tests: no Cube, no Temporal needed
make lint
make verify                   # lint + test + cube-smoke
make integration-test         # Cube + Temporal integration suites

# ── Run the factory ─────────────────────────────────────────────────────────
make factory-worker           # start the Temporal worker
make factory-run-demo         # the Phase 1 acceptance test, end to end
```

## Running one task

```bash
./bin/factory run \
  --repo https://github.com/your-org/your-repo \
  --rev <exact-revision> \
  --task "Change the greeting from hello to hello factory" \
  --agent opencode2 \
  --verification default
```

It prints the run id and workflow id immediately, then on completion:

```
FACTORY RESULT: SUCCEEDED
AGENT RESULT:   SUCCESS
VERIFICATION:   PASSED
CLEANUP:        PASSED

Patch:
.factory/runs/run-…/changes.patch
```

`--local-path <dir>` packs a repository that exists on the factory host as a git
bundle, so the whole flow works offline and without touching a remote. That is
how the acceptance fixture runs.

Inspect a run:

```bash
./bin/factory status <run-id>
./bin/factory logs   <run-id>
```

## Evidence produced by every run

```
.factory/runs/<run-id>/
  task.json          the request as accepted, with a task hash and resolved resources
  baseline.json      source, requested revision, resolved SHA, branch, status
  git-before.txt     exactly what the agent started from
  changes.patch      the deliverable
  git-after.txt      post-run SHA and status
  run.json           the manifest: identity, environment, resources, conditions, three outcomes
  inventory.json     the exact environment document the agent was given
  cleanup.json       destroy outcome, independently verified
  audit/actions.jsonl  operator actions (attach, preview, review decisions)
  agent/             prompt, stdout, stderr, result (all redacted)
  verification/      per-gate result, stdout and stderr (all redacted)
```

Changes are stored beside the runs, not inside them:

```
.factory/changes/<change-id>.json   identity, activations, lineage, aggregates
```

## Resources and the work item

Three ideas from Google's AX shape the factory layer (see
[ADR 0001](adr/0001-ax-inspired-resources.md)):

1. **Runs reference declared resources.** `[workspaces]`, `[models]`,
   `[egress]`, `[budgets]` and `[scopes]` are operator policy; a run names them
   and the resolved set with its digest lands in `run.json`. A scope can only
   narrow the global policy.
2. **A change owns identity.** Runs are activations of a durable change, so
   rework carries lineage and spend aggregates instead of starting from zero.
3. **The sandbox inventory is the harness contract.** Every agent starts with
   `/workspace/.factory/inventory.json` describing its repository, revision,
   workspace, egress policy and model — names and digests, never credentials
   and never the task text.

A run pauses for a human review gate when `[review] enabled = true`; the
sandbox is checkpointed while paused (recorded as `suspend_result`), preview
URLs are published (or `preview_result = SKIPPED` explains why they were not),
and a timeout is recorded as `timeout` rather than implied. Rejection notes are
redacted into the manifest and carried to the durable change as its rework note.
Attach and preview are default closed and audited.

## Security model in one table

| Trusted (operator) | Untrusted (task / repository) |
| --- | --- |
| Which repositories are allowed | The task text |
| Which harnesses exist, and their executables and arguments | A requested approved repository |
| Which sandbox template is used | A requested revision |
| Verification profiles | Repository content |
| Resource limits and credentials | |

A caller can name an approved repository at an approved revision with a task.
It can never name an executable, a host path, an environment variable, or a
credential. See `docs/adr/0006-control-plane-security-boundary.md`.

## Documentation

| Document | Contents |
| --- | --- |
| [`docs/upstream.md`](docs/upstream.md) | Upstream URLs, commits and pinned dependency versions |
| [`docs/architecture/current-state.md`](docs/architecture/current-state.md) | What Machinist does today, analysed package by package |
| [`docs/architecture/target-state.md`](docs/architecture/target-state.md) | The architecture this project implements, and its Phase 2 seams |
| [`docs/adr/`](docs/adr/) | Six architecture decision records |
| [`docs/operator-guide.md`](docs/operator-guide.md) | Run, inspect, cancel, extend, and troubleshoot |
| [`docs/backlog/phase-2.md`](docs/backlog/phase-2.md) | The prioritised Phase 2 backlog |

## A required credential

The factory runs the coding agent **inside** the microVM. Sandboxes cannot reach
the factory host, so a host-local model endpoint is not usable from an agent run.
Production Unreal runs use CubeEgress to authenticate at the network boundary;
the real provider credential is not placed inside the sandbox.

That credential is operator-provisioned configuration, never task-controlled:

```toml
[harnesses.opencode2]
pass_env       = ["MY_PROVIDER_KEY"]                              # env allowlist
provider_files = { "<sandbox path>" = "<host path>" }             # opt-in staging

[harnesses.unreal]
credential_mode = "cube_egress"                                  # recommended
```

See the operator guide §8 for the trade-offs and rotation procedure. A missing
CubeEgress credential fails the network-lockdown activity before the agent can
run, and the sandbox is still destroyed.

---

Machinist's original documentation follows.
