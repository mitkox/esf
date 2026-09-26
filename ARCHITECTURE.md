# Architecture

Machinist owns process execution, not orchestration.

- `config.toml` defines portable named commands, optional prompt templates, timeouts,
  triggers, and server settings.
- `worker.toml` defines approved executor argument arrays and logical repository paths.
- `internal/runner` starts one process in one repository, writes the prompt to stdin,
  streams both output channels, records artifacts and token usage, and terminates the
  process tree on timeout or cancellation.
- `internal/controlplane` stores one job and one run, leases it to a capable worker,
  rejects stale completions, and exposes authenticated APIs and the web UI.
- `internal/managedworker` resolves only worker-owned executor and repository names.

Each job has exactly one run. The database enforces this with a unique `runs.job_id`.
Terminal state comes only from the process result. There is no internal stage model.

## The factory layer

The Temporal-based factory in `internal/factory` sits beside Machinist, not inside it:

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

## Optional assurance control plane

QMS is enabled only by configured `quality.policy_files`. Without them, new
workflows route to the standard factory lifecycle, and no quality store or
socket is opened. Existing histories retain their command sequence through
Temporal workflow versioning. Standard mode continues to support Linux and
macOS; the authenticated QMS worker requires Linux.

`internal/assurance` defines policies, risk classification, immutable records,
approval decisions, non-conformances, CAPA and provider contracts. The local
provider uses embedded SQLite; an enterprise service is not required.
`internal/factory/quality_*` integrates those records with Temporal, fresh
verification/review sandboxes and a Unix socket authenticated by kernel UID.
Agents and client-supplied actor names have no authority to approve a run.

For controlled runs, content-addressed evidence is published before one SQLite
transaction commits the decision, manifest, unsigned attestation, audit event
and export outbox. Compatibility files are repairable exports. Cleanup precedes
human approval, and patch readiness never authorizes release. The
[assurance ADR](docs/adr/0004-assurance-control-plane.md) describes the complete
authority, recovery and isolation boundaries.
