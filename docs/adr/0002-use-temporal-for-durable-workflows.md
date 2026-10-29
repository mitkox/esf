# ADR 0002 — Use Temporal for durable workflows

- **Status:** Accepted
- **Date:** 2026-09-19
- **Deciders:** Factory engineering
- **Update (0.6.2):** The Machinist control plane referenced below was removed;
  see [ADR 0008](0008-remove-machinist.md). The comparisons are retained as the
  design rationale that led to this decision.

## Context

A factory run has many steps, several of which are slow, fallible, and must be
retried independently: create a microVM, provision a toolchain, clone a
repository, run a coding agent for up to tens of minutes, run deterministic
gates, extract a patch, destroy the microVM.

Machinist's architecture is explicit that it does not model this:

> Each job has exactly one run. The database enforces this with a unique
> `runs.job_id`. Terminal state comes only from the process result. There is no
> internal stage model.

Extending Machinist into a general DAG engine would contradict a design
constraint its author deliberately chose, and would mean re-implementing
durability, retries, timers and cancellation — the parts that are hardest to get
right and least differentiated for this product.

Meanwhile, runs are long. A worker restart, a host reboot, or a cancelled
request must not leave a half-applied change or a leaked microVM.

## Decision

Use **Temporal** as the durable orchestration engine, with the official current
development stack from `temporalio/samples-server` (the archived
`temporalio/docker-compose` repository is not used; see ADR-adjacent notes in
`deployments/dev/temporal/README.md`).

Workflow code performs **no** network, filesystem or process operation.
Every external effect is an activity, which is what makes Temporal replay
deterministic. Activities take and return serializable values; sandboxes are
referenced by ID, never as live handles.

Machinist's control plane is left intact. The factory adds a parallel control
plane; the two can coexist in one process and neither owns the other.

## Alternatives considered

| Alternative | Rejected because |
| --- | --- |
| Extend Machinist's job/run store into a stage engine | Fights a deliberate design constraint; re-implements retries, timers, cancellation and history. |
| PostgreSQL-backed custom job queue | Solves scheduling but not durable multi-step state, timers, or replay. |
| Cadence | Temporal is the actively developed successor and the current official samples reference. |
| Airflow / Argo Workflows | Batch-oriented, heavier, and would pull in Kubernetes. |
| A single long-running activity (`run.sh`) | Simplest to write, but loses per-step retry, per-step evidence, and cancellation semantics — and would leave cleanup to a shell trap. |
| In-process goroutines with a homegrown retry loop | No durability across restarts, which is the whole point. |

## Consequences

**Positive**

- Per-step retry policies are declarative and typed: deterministic steps get one
  attempt, infrastructure five with exponential backoff, the agent exactly two.
- Cancellation is a first-class operation, and cleanup runs from a **disconnected
  context**, so cancelling a run still destroys its microVM.
- Workflow history is an audit trail of every step, attempt and timer.
- The Temporal UI (`http://127.0.0.1:8233`) is free operational visibility
  without building a UI.

**Negative / accepted**

- A new runtime dependency: Temporal server + PostgreSQL for development.
- Workflow code must be deterministic, so no logging of wall-clock time, no
  random values, no direct I/O. This is a real discipline cost, paid once, and
  enforced by keeping all effects in activities.
- Activity payloads are size-limited, so large data (agent output) is stored as
  artifacts and deliberately **not** passed through workflow history.

## Security implications

- **Temporal is bound to `127.0.0.1` only.** The upstream compose publishes on
  `0.0.0.0`; this project's compose publishes every port to loopback. The
  Temporal gRPC endpoint is not reachable off-host.
- Host ports are moved off their defaults (`5433`, `8233`) because `5432` and
  `8080` are already occupied on this machine — an accidental collision would
  otherwise be mistaken for a Temporal fault.
- Workflow history contains task text and repository identifiers but **not**
  agent output, and credentials are never placed in activity inputs.
- No Elasticsearch/OpenSearch is deployed: PostgreSQL provides both persistence
  and visibility, so there is no additional network-accessible datastore.
- Development database credentials are development-only and are not secrets.

## Future implications

- Phase 2 fan-out is a workflow change, not an infrastructure change:
  `workflow.Go` over N clones, each running the existing activity sequence.
- The `candidate_id` and `snapshot_id` manifest fields already exist, so a
  parallel run produces comparable per-candidate manifests.
- Temporal schedules and cron would replace Machinist's trigger scheduler if the
  factory ever needs scheduled runs; not required for the MVP.
- A production deployment would run Temporal with its own persistence and TLS;
  the development stack here is deliberately loopback-only and single-node.
