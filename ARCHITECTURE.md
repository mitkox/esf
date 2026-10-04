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
