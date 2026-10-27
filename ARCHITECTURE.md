# Architecture

Machinist owns staged jobs and their workflow steps. Temporal owns factory
runs. A Machinist review does not approve factory decisions.

- `config.toml` defines portable named commands, optional prompt templates, timeouts,
  triggers, and server settings.
- `worker.toml` defines approved executor argument arrays and logical repository paths.
- `internal/runner` starts one process in one repository, writes the prompt to stdin,
  streams both output channels, records artifacts and token usage, and terminates the
  process tree on timeout or cancellation.
- `internal/controlplane` stores jobs, step attempts, immutable shared artifacts,
  review gates, and execution leases. It rejects stale completions and exposes
  authenticated APIs and the web UI.
- `internal/managedworker` resolves only worker-owned executor and repository names.

Single-command jobs keep their original semantics. Workflow jobs can advance
through several steps, with separate attempts and review gates. Schema 2
databases migrate to schema 5 with a consistent backup before migration.

## The factory layer

The Temporal-based factory in `internal/factory` sits beside Machinist:

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

### Optional candidate security assessment

An explicitly scope-granted security profile can add advisory evidence after
functional verification and before human review. The versioned workflow records
only small references and summaries; activities capture bounded exact candidate
bytes and own all allocation, transfer, assessment and teardown effects.

`internal/securityassessment` drives a fixed OpenCyber stdio tool allowlist in a
dedicated assessor Cube VM against a separate disposable target VM. A guarded
factory-side endpoint binds the target instance, port and permitted requests.
Control-plane credentials stay with the factory. Raw candidate and evidence
bytes remain in private storage, outside ordinary run artifacts; exported
evidence must match independently held identities and hashes.

Coverage, assertion outcome, owned-resource cleanup and human review remain
separate. Heartbeats deliver cancellation, and workflow progression waits for
activity cleanup acknowledgment. The feature is disabled unless an operator
supplies pinned qualification and executor configuration. See
`docs/security-assessment.md` for the profile and evidence contracts.

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
