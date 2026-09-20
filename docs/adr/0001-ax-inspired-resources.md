# ADR 0001 — AX-inspired resources, lifecycle and change identity

Status: accepted
Date: 2026-09-20
Context: ESF v0.1.0 (Machinist fork + Temporal factory + CubeSandbox)

## Problem

Three gaps limited the factory to a single, isolated run:

1. **The run was the unit of work.** A review-and-rework cycle was a brand-new
   run with no lineage, no aggregate spend, and no way to answer "what happened
   to this change across three attempts".
2. **Everything the agent needed was recomputed per run.** Packages, setup and
   clone happened inside every fresh microVM, and the environment an agent saw
   was implied by prompt text and harness assumptions rather than declared.
3. **A finished sandbox was destroyed immediately.** The most expensive moment —
   a live microVM holding a built repository — could not survive a human
   decision, and no human could inspect a running change.

Google's AX (`github.com/google/ax`) was studied as prior art. AX is an
orchestration layer over Agent Substrate; ESF is not adopting it as a
dependency (two pre-1.0 projects on the critical path of an enterprise product
is not acceptable). The value taken is the *model*: declarative resources,
durable work items with lifecycle, discoverable environments, and explicit
default-closed debug surfaces.

## Decisions

### 1. Resources are declared, runs reference them

New operator-declared resource families in `factory.toml`: `[models]`,
`[egress]`, `[workspaces]`, `[budgets]`, `[scopes]`, plus `[review]`.

A `RunRequest` names them; it can never define one. Resolution happens **exactly
once**, in the `ValidateRequest` activity, and the result travels through the
workflow as plain data (`ResolvedResources`). Workflow code never resolves a
name, because a config change mid-run would make Temporal replay
non-deterministic.

Every resolved resource carries a SHA-256 content digest, recorded in the run
manifest. `workspace=python` is not reproducible; `workspace=python@sha256:…`
is. Credential *values* never enter a digest or a manifest — only the
environment variable name.

**Scopes narrow, never widen.** A scope repository list is intersected with the
global allowlist by prefix coverage, so a scope may declare a narrower prefix
but can never introduce one the operator did not approve.

### 2. A Change owns identity; Runs are activations

`internal/factory/change.go` introduces a durable `Change` stored as JSON beside
the run artifacts (`<data_dir>/changes/<id>.json`). Runs are activations of it.

- A run without a `change_id` is its own change, preserving MVP behaviour.
- Rework carries `parent_run_id`; the activation reason is recorded.
- Aggregates (attempts, cost, tokens) are **recomputed from the durable run
  manifests**, never cached and trusted, and a harness that reports nothing
  contributes zero.
- `RecordRun` is idempotent by run ID: a retried Temporal activity cannot
  inflate attempts or spend.
- A terminal human decision (`DONE`, `ABANDONED`) is not overwritten by a later
  run; reopening is explicit.
- Recording the change is best-effort: the manifest is authoritative, and a
  derived index failure must not turn a successful run into a failed one.

### 3. The sandbox inventory replaces a metadata service

AX runs a metadata server inside the sandbox. ESF writes a document instead:
`<repo-parent>/.factory/inventory.json`, fixed by contract
(`InventoryContractVersion = "factory/v1"`).

A listening service inside a sandbox is a privileged component that widens the
threat model for no benefit here. The same goal is met with a file written by
the factory before the agent starts: repository + resolved SHA, workspace and
digest, effective egress policy, model (names and digests only), limits, sandbox
identity, and the **path** of the task file.

The task text is never copied into the inventory: it is delivered once, by the
existing stdin-file mechanism, so untrusted text has exactly one delivery path.

A failure to write the inventory fails the run. Continuing would mean an agent
running blind while the manifest claimed its environment was described.

### 4. Conditions make step outcomes legible

`RunManifest` and the `status` query now carry `Conditions` (AX-style): one
tri-state entry per step (`Validated`, `SandboxReady`, `WorkspacePrepared`,
`RepositoryPrepared`, `InventoryWritten`, `AgentCompleted`, `Verified`,
`ArtifactsCollected`, `ReviewPaused`, `CleanupVerified`, `BudgetExceeded`,
`EvidenceWritten`).

Conditions are set in **workflow code**, using `workflow.Now`, so replay
reconstructs the same list and timestamps. A failed condition is never removed.

`EvidenceWritten` is status-only: it is set after the manifest is durable, so it
cannot be part of the manifest it describes.

### 5. The review gate suspends instead of destroying

When `[review] enabled = true` (or a scope/run override), the workflow pauses
after the deterministic gates report and the deliverable patch is captured:

1. publish preview URLs while the sandbox is definitely awake;
2. checkpoint the sandbox (`SuspendSandbox`) when the provider advertises the
   capability;
3. wait for the `review-decision` signal or a timeout;
4. wake the sandbox (`ResumeSandbox`) before anything else uses it.

Honesty rules:

- a provider without suspend capability records `SuspendResult = SKIPPED` with a
  reason; "paused but still paying for the VM" and "paused and free" are
  different operational facts;
- a provider without preview capability records `PreviewResult = SKIPPED` with
  a reason rather than silently omitting the requested exposure;
- a timeout is recorded as `timeout`, not implied by an empty field, and the run
  finishes normally;
- a resume failure is fatal, because every later activity needs a live sandbox;
- a human rejection does **not** falsify the gate result: `FactoryResult` stays
  whatever the deterministic gates decided, while `HumanResult = rejected`
  returns the change to `OPEN`.

### 6. Attach and preview are default-closed and audited

`[sandbox] allow_attach` / `allow_preview` default to `false`. When enabled,
every invocation appends a JSONL entry to `audit/actions.jsonl` in the run's
evidence (actor, action, outcome, non-secret detail). Command output is not
stored; the argv is recorded by length and program only, because arguments may
contain a pasted token.

Neither action resumes a suspended sandbox implicitly. The Cube SDK's `Connect`
auto-resumes paused sandboxes, so both paths check state through the read-only
list endpoint first (`sandbox.Stater`). Waking a sandbox must be a deliberate
decision.

## Consequences

- Runs gain a parent, lineage and aggregate spend; the CLI gains a uniform
  `get`/`describe`/`apply`/`delete` vocabulary plus `review`, `attach`,
  `preview`, and `status --watch`.
- Operators must declare resources to use them; unknown names are rejected
  rather than silently falling back to "no policy".
- The manifest grows but stays backward compatible: every new field is
  `omitempty` and the MVP path (no resources, no review) is unchanged.
- Cube suspend/resume is now exercised; the capability is advertised because it
  is implemented and tested, unlike snapshot/clone which remain declared seams.

## Rejected

- **Adopting AX or Agent Substrate as a dependency.** Two pre-1.0 APIs on an
  enterprise critical path; ESF keeps its provider boundary.
- **Replacing Temporal with Redis Streams.** Temporal's deterministic replay and
  typed retries are strictly better for this workload.
- **kubectl-style CRDs.** ESF is not Kubernetes-native; TOML resources plus
  JSON/TOML run manifests give the same reviewability without the machinery.
- **Actor multiplexing of hot runs.** Oversubscription is safe for idle,
  suspended work — which is what the review gate does — not for a run holding a
  build's worth of RAM.
- **A long-lived metadata service inside the sandbox.** See decision 3.
