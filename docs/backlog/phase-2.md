# Phase 2 Backlog

Phase 1 is complete: one task in, one isolated Cube microVM, one coding agent,
deterministic verification, one verified patch out.

Phase 2 is **high-density speculative software engineering**:

```
prepare environment once → snapshot → clone N ways → N independent agents
                        → deterministic eval → strongest result
```

The architectural rule that must hold:

> **The factory controls parallelism and infrastructure. Agents operate only
> inside the Cube assigned to them.**

Wrong: an agent decides to clone a Cube.
Correct: the *workflow* snapshots, clones and assigns.

---

## P0 — The speculative core

### P0.1 Cube snapshot abstraction

**Why.** "Prepare once, branch many" is the economic core of speculative
execution. Preparing a repository and toolchain once and then snapshotting avoids
paying that cost 8–16 times.

**What.** Implement `sandbox.Snapshotter.Snapshot` on the Cube provider, using
`Sandbox.CreateSnapshot`. Flip `Capabilities.Snapshot` to true **only when the
integration test passes**. Record `snapshot_id` in the manifest, which already
has the field.

**Acceptance.**
- An integration test snapshots a prepared sandbox, rolls back, and proves the
  working tree matches the snapshot.
- A snapshot of a sandbox with a prepared repository clones faster than
  re-preparing from scratch; the measured difference is recorded.
- A failed snapshot does not fail the run; the workflow falls back to
  prepare-per-candidate and records the degraded path.

**Seams already present.** `sandbox.Snapshotter`, `Capabilities.Snapshot`,
`RunManifest.SnapshotID`.

### P0.2 Cube clone / fan-out abstraction

**Why.** Speculative breadth is only useful if N clones are cheap enough to be
worth it.

**What.** Implement `Snapshotter.Clone(ctx, sandboxID, snapshotID, n)` using
`Sandbox.Clone`, which returns N sandboxes. Set `Capabilities.Clone` and
`CloneMultiple`. Record `clone_parent` per candidate.

**Acceptance.**
- Cloning one prepared sandbox into 4 and 8 targets works, and every clone runs a
  command independently (no cross-talk).
- **Every clone is destroyed**, verified by an independent `List`; a leaked clone
  fails the test.
- Clone failure mid-way destroys the clones already created.
- Measured: clone latency and marginal cost per clone.

**Seams already present.** `Capabilities.CloneMultiple`, `RunManifest.CloneParent`.

### P0.3 Parallel candidate execution

**Why.** The winning strategy is breadth, not one enormous sequential agent.

**What.** Add a `SpeculativeChangeWorkflow` (or a `strategy` branch in the
existing one) that:

1. prepares the repository once and snapshots it,
2. clones N candidates,
3. runs the **existing** activity sequence per candidate concurrently using
   `workflow.Go` + `workflow.Await`,
4. collects per-candidate evidence,
5. verifies each candidate.

Agents are unchanged: each receives a sandbox ID and runs inside it. No agent
knows that siblings exist.

**Acceptance.**
- 4- and 8-way fan-out completes; total wall-clock is materially below N×
  sequential.
- One candidate failing does not abort the others.
- Cancellation destroys **every** live clone, not just the first.
- `candidate_id` is present in every per-candidate artifact and manifest.

**Seams already present.** `RunManifest.CandidateID`, `Strategy`; all steps are
already activities so fan-out needs no restructuring.

### P0.4 Deterministic candidate evaluation

**Why.** Ranking must not be an LLM opinion, for the same reason verification is
not.

**What.** A pure function over already-recorded deterministic signals:

| Signal | Source |
| --- | --- |
| Mandatory gates passed | `verification/result.json` |
| Number of gates passed | same |
| Diff size / files touched | `changes.patch` |
| Changed files overlap with test files | `changes.patch` |
| Candidate completed without infrastructure error | manifest |

A candidate is eligible only if **every mandatory gate passed**. Ranking is
lexicographic and total, so it is reproducible; there are no ties broken by
"model confidence".

**Acceptance.**
- The same candidate set always yields the same ranking.
- An ineligible candidate can never be selected, regardless of diff size.
- The ranking rationale is recorded per candidate (`evaluator_score` plus the
  winning signals) so a human can audit why one candidate won.

**Seams already present.** `RunManifest.EvaluatorScore`, `CandidateID`.

---

## P1 — Breadth and quality

### P1.1 Codex harness
An `[harnesses.codex]` configuration, plus a provisioning variant if the binary
shape differs. No workflow change. **Acceptance:** Codex produces a verified
patch on the fixture repository.

### P1.2 Claude Code harness
As above for Claude Code. **Acceptance:** same.

### P1.3 Different harness/model per stage
Make the harness name a per-stage property (implementation vs review vs repair),
so a cheap model can plan and an expensive one can implement. **Acceptance:** a
single run uses two different harnesses and records both in the manifest.

### P1.4 Reviewer stage
A review stage over the winning candidate, producing `review_findings` — advisory
and recorded, never a substitute for the deterministic gates. **Acceptance:** a
review finding is recorded without altering `factory_result`.

### P1.5 Bounded repair loop
On verification failure, feed the gate output back to the agent for at most
`maxIterations`, then re-verify. **Acceptance:** the loop terminates; each
iteration is a distinct recorded attempt; the loop can never turn a failing gate
into a pass without the gate actually passing.

### P1.6 `factory.yaml`
The repository-declared factory configuration sketched in the design. Must be
**operator-validated**: a repository must not be able to relax the gates it is
judged by. **Acceptance:** an invalid or unsafe file is rejected with a clear
error; a valid one drives verification.

### P1.7 Credential injection at the egress proxy
**Completed for the Unreal harness.**
Remove the need to stage provider credentials into sandboxes, closing the only
material limitation of Phase 1 (completion report §11.1). This deployment's Cube
egress proxy already supports L7 credential injection. **Acceptance:** an agent
run authenticates to a model provider with **no** credential present anywhere in
the sandbox, proven by grepping the sandbox filesystem and environment.

---

## P2 — Platform

| # | Item | Notes |
| --- | --- | --- |
| P2.1 | GitHub PR creation | First remote mutation. Requires a human-approval gate before creation. |
| P2.2 | Azure DevOps support | Repository provider extension. |
| P2.3 | GitLab support | Repository provider extension. |
| P2.4 | OIDC / RBAC | Replaces the single-operator assumption; attaches identity to "who may request what". |
| P2.5 | Vault integration | Short-lived, audited credentials replacing `pass_env` and `provider_files`. |
| P2.6 | OPA policies | Expresses allowlists as reviewable policy instead of TOML. |
| P2.7 | S3/MinIO artifact store | `ArtifactStore` is already domain-free, so this is an implementation, not a refactor. |
| P2.8 | OpenTelemetry dashboards | Metrics and the trace shape already exist. |
| P2.9 | Evaluation / history database | Ingests `run.json` across runs to answer the product questions. |

---

## What the evidence model must eventually answer

Every item above should be justified by one of these questions. If a proposed
Phase 2 feature does not help answer one, it probably is not Phase 2:

- Which agent is best for this repository?
- Which model is best for this class of task?
- How often does an agent pass on first attempt?
- How often do humans modify its PR?
- What is the cost per accepted PR?
- Which factory configuration produces fewer regressions?
- Does 8-way speculative execution beat one expensive agent?
- Which golden Cube template produces the highest success rate?

The Phase 1 manifest already carries `model`, `model_provider`, `tokens_in`,
`tokens_out`, `inference_cost`, `candidate_id`, `strategy`, `evaluator_score`,
`review_findings` and `human_result` so that these become data questions rather
than schema migrations.
