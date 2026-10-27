# ADR 0005 — Factory artifact model

- **Status:** Accepted
- **Date:** 2026-09-19
- **Deciders:** Factory engineering
- **Update (0.6.2):** the artifact store package is now
  `internal/factoryartifacts`; the Machinist store referenced in this record was
  removed (see [ADR 0008](0008-remove-machinist.md)). The decision is unchanged.

## Context

The product is not "run an AI coding CLI remotely". It is a durable, measurable
software engineering production system that must eventually answer:

- Which agent is best for this repository?
- Which model is best for this class of task?
- How often does an agent pass on first attempt?
- How often do humans modify its PR?
- What is the cost per accepted PR?
- Which factory configuration produces fewer regressions?
- Does 8-way speculative execution beat one expensive agent?

None of those questions can be answered from logs. They require **durable,
structured, per-run evidence**.

Three facts force the design:

1. The microVM is ephemeral. Anything left in it is lost.
2. Temporal holds *workflow* state, not product evidence, and its payloads are
   size-limited — agent output can be megabytes.
3. A coding agent can echo an environment variable or a credential into its
   output, and that output becomes durable evidence.

## Decision

Separate **workflow state** from **product evidence**, and store evidence in a
dedicated, durable, domain-free artifact store.

**Store** (`internal/factoryartifacts`): bytes at validated relative paths, scoped per
run. It knows nothing about runs, states or manifests — which is what makes a
future S3/MinIO backend a drop-in replacement. Every path is validated against
traversal, and run IDs must be plain path segments. Opening a run store is
idempotent and **non-destructive**, so a retried activity cannot erase the
evidence of an earlier attempt.

**Layout**, per run under `<data_dir>/runs/<run-id>/`:

```
task.json · baseline.json · git-before.txt · run.json · changes.patch
git-after.txt · cleanup.json
agent/{prompt.txt,stdout.log,stderr.log,result.json}
verification/{result.json,<gate>.stdout,<gate>.stderr}
```

**Manifest** (`run.json`) records identity, input, environment, timing, the
three separate outcomes, and artifact pointers — and carries Phase 2 extension
fields (`model`, `model_provider`, `tokens_in`, `tokens_out`, `inference_cost`,
`snapshot_id`, `clone_parent`, `candidate_id`, `strategy`, `evaluator_score`,
`review_findings`, `human_result`) so speculative execution does not require
rewriting it.

**Redaction on write.** `factory.Redactor` scrubs (a) exact secret values
registered by the operator at startup — including the contents of any
provisioned provider file — and (b) credential-shaped patterns: bearer tokens,
`Authorization` headers, `sk-…`, `ghp_…`, `xox…`, `AKIA…`, `AIza…`, credentials
embedded in URLs, and `*_PASSWORD=…`-style assignments. It runs on every
artifact write path, including the verification sink.

**Manifest is written after cleanup**, so `run.json` always contains the cleanup
result. A manifest reporting "cleanup never attempted" is not auditable.

## Alternatives considered

| Alternative | Rejected because |
| --- | --- |
| Keep evidence in Temporal history | Payload limits; evidence would be lost when history is retained out. Also mixes product data with a workflow engine. |
| Keep evidence inside the microVM | The microVM is destroyed by design. |
| One big `run.json` containing everything | Agent output reaches megabytes; a single document is not reviewable and would be rewritten repeatedly. |
| Store evidence in Machinist's store | Machinist owns single-process execution state; product evidence has different lifetime and query needs. |
| Add an evaluation database now | Explicitly out of scope for the MVP. The manifest is designed so it can be ingested later. |
| Store only the patch | A patch without the baseline, the task, the gate results and the cleanup outcome cannot be audited or compared. |

## Consequences

**Positive**

- Every run is reproducible from recorded metadata: exact revision, baseline
  SHA, task hash, harness, template, and gate results.
- Evidence survives sandbox destruction, worker restart and host reboot.
- The patch is a first-class deliverable at a predictable path.
- Phase 2 comparison is a data question: manifests are already comparable.

**Negative / accepted**

- Local filesystem storage grows without bound; retention/pruning is not
  implemented in the MVP.
- A run whose artifact write fails cannot retry that write in isolation; the
  workflow records the gap in `run.json`'s `error` field rather than silently
  dropping it.
- Local-directory storage assumes a single host. Multi-host workers would need
  the S3/MinIO backend.

## Security implications

- **Evidence is treated as sensitive.** Files are written `0640`, the root is
  `0750`, and traversal is rejected in both the run-ID and relative-path
  dimensions.
- **Redaction is defence in depth.** The first line of defence is not putting
  secrets where the agent can see them; the second is allowlisting the
  environment; redaction is the last, applied to bytes on their way to disk.
- **Redaction preserves diagnostic context** (the header or key name survives,
  only the value is masked), so evidence stays useful after scrubbing.
- The `agent/prompt.txt` artifact is recorded deliberately: auditing what the
  agent was asked is a requirement, and it is redacted like everything else.
- Cube management credentials never appear in any artifact.

## Future implications

- An `ArtifactStore` S3/MinIO implementation is the P1/P2 item this interface
  exists for; no caller changes.
- An evaluation database ingests `run.json` across runs to answer the product
  questions in the Context section.
- `evaluator_score`, `review_findings` and `human_result` are already present,
  so review loops and human acceptance feed the same record.
