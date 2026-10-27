# Architecture

The factory is a Temporal-coordinated software production system. A task is one
activation of a durable `Change`; each run executes a coding agent in its own
CubeSandbox microVM, verifies the result with deterministic gates, and preserves
the patch and evidence on the factory host.

```text
Task → Temporal → CubeSandbox → coding agent → verification → patch + evidence
```

The `factory` CLI is the control plane and the security boundary. It accepts
only an approved repository, an exact revision and task text; it never accepts
an executable, a host path or an environment override. The factory has no HTTP
API or web UI. (The separate private sf turn service below is not a second
control plane.)

## The factory layer

- `internal/factory/resources.go` resolves operator-declared resources
  (`[workspaces]`, `[models]`, `[egress]`, `[budgets]`, `[scopes]`) exactly once, in
  the validation activity. Workflow code never resolves a name, because a config change
  mid-run would break Temporal replay. Scopes narrow global policy, never widen it.
- `internal/factory/change.go` owns the durable work item. A run is an activation of a
  `Change`; aggregates are recomputed from the durable run manifests and never
  estimated.
- `internal/factory/inventory.go` writes the harness contract document into the sandbox
  before the agent starts. The task text is delivered by stdin-file as before and is
  never copied into the inventory.
- `internal/factory/workflow.go` records conditions per step and, when review is
  enabled, pauses after the gates report, checkpoints the sandbox (capability-gated),
  waits for the `review-decision` signal or a timeout, then resumes before continuing.
- `internal/sandbox/lifecycle.go` declares the optional `Suspender`, `Previewer` and
  `Stater` capabilities. The factory records `SKIPPED` with a reason when a provider
  lacks one, so evidence never claims a cost was saved when it was not.
- `internal/factoryartifacts` stores runs, changes, patches and evidence under
  `.factory/`. Evidence is durable on the factory host, never only inside a microVM.
- `internal/agentharness` resolves operator-registered harnesses; callers name a
  harness, never an executable.
- `internal/verification` executes the configured build and test gates and reads exit
  codes only; the model never declares success.
- `internal/repository` prepares the exact revision inside the sandbox.
- `internal/threatmon` quarantines out-of-bounds agent behavior at execution time.

The distinction that matters most:

```text
agent_result        = SUCCESS
verification_result = FAILED
factory_result      = VERIFICATION_FAILED
```

An agent saying it succeeded is never enough.

## sf execution boundary

`cmd/turn-executor` and `internal/turnexec` expose a private named-profile turn
API over the existing Cube adapter. sf owns workflow ordering, workspaces, asks,
connector grants and acceptance. This service owns only a fresh VM per turn,
immutable binary checkpoint references, replayable bounded output, ownership
leases, bound permission decisions and verified cleanup. It does not create a
second Temporal workflow. Active records fail closed after service restart;
provider metadata reconciles lost create responses without repeating execution.

Profiles are operator-owned and have immutable digest receipts. Compiled turns
also require an exact policy digest. API clients cannot choose commands, host
paths, templates or secrets. See `docs/sf-turn-executor.md` for supported
constraints, deployment, cost evidence and retained live qualification limits.

## History

ESF began as a fork of [Machinist](https://github.com/owainlewis/machinist). The
inherited `machinist` CLI, local control plane, SQLite store, managed worker and
web UI were removed in 0.6.2; the factory architecture above is the only
supported surface. See [ADR 0008](docs/adr/0008-remove-machinist.md) and
[docs/upstream.md](docs/upstream.md).
