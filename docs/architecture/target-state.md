# Target State

The architecture the factory implements, and the seams that make Phase 2
speculative execution an addition rather than a redesign.

---

## 1. Shape

```
                    CLI / API
                  cmd/factory
                       │
                       ▼
              Factory Control Plane
         internal/factory (config, policy, states)
                       │
                       ▼
                 Temporal Client
              go.temporal.io/sdk/client
                       │
                       ▼
              SoftwareChangeWorkflow          ← durable, deterministic
                       │
                 activity queue "factory"
                       │
                       ▼
                Factory Worker
         internal/factory (activities)
                       │
       ┌───────────────┼───────────────┐
       ▼               ▼               ▼
  Repository       Agent            Verifier
   Provider        Harness
internal/repository  internal/agentharness  internal/verification
       │               │               │
       └───────────────┼───────────────┘
                       │
                       ▼
                Sandbox Provider
             internal/sandbox (interface)
                       │
                       ▼
                  CubeSandbox
          internal/sandbox/cube (SDK adapter)
                       │
                       ▼
                  isolated microVM
             ┌──────────────────────────┐
             │  Debian 12, own kernel   │
             │  /workspace/repository   │
             │  coding agent            │
             │  build + test tools      │
             └──────────────────────────┘
```

Durable data, split by owner:

| Store | Holds | Survives |
| --- | --- | --- |
| Temporal | Workflow state, retries, timers, history | Worker restart, host reboot |
| Factory artifact store (`internal/factoryartifacts`) | Patches, logs, verification evidence, manifests | Sandbox destruction, process restart |
| CubeSandbox | Nothing durable the factory depends on | — |

**The Cube microVM remains ephemeral.** No artifact the factory needs is stored
only inside a sandbox.

## 2. Package boundaries

| Package | Responsibility | Must not |
| --- | --- | --- |
| `internal/sandbox` | Sandbox lifecycle abstraction, `argv` quoting, fake provider | Import Cube types |
| `internal/sandbox/cube` | Cube SDK adapter, config, error classification | Expose SDK types past its boundary |
| `internal/agentharness` | Harness registry, provisioning, invocation, output capture | Decide policy, know about Temporal |
| `internal/repository` | Repository policy, acquisition into a sandbox, diff extraction | Execute on the host |
| `internal/verification` | Structured-argv gates, pass/fail from exit codes only | Accept a shell string |
| `internal/factoryartifacts` | Durable byte storage with path validation | Know about runs, states or manifests |
| `internal/factory` | Policy, run identity, states, workflow, activities, CLI wiring | Contain vendor SDK types |

The dependency graph is acyclic and points inward:
`factory → {sandbox, agentharness, repository, verification, factoryartifacts, cube}`.
No lower layer imports `factory`.

## 3. The workflow

```
SoftwareChangeWorkflow(ctx, RunRequest) (RunManifest, error)

  ValidateRequest        ← operator policy; no sandbox exists yet
        ↓
  CreateSandbox          ← one microVM per run
        ↓
  PrepareSandbox         ← factory base packages, then harness provisioning
        ↓
  PrepareRepository      ← clone or bundle; check out the exact revision
        ↓
  RunAgent               ← agent executes INSIDE the microVM
        ↓
  RunVerification        ← deterministic gates, structured argv
        ↓
  CollectArtifacts       ← patch, post-run SHA, git-after
        ↓
  DestroySandbox         ← always, from a disconnected context
        ↓
  FinalizeResult         ← durable manifest, written AFTER cleanup
```

Properties the implementation guarantees:

1. **No I/O in workflow code.** Network, filesystem and process calls are all in
   activities. This is what makes Temporal replay deterministic.
2. **Cleanup on every exit path** — success, agent failure, verification failure,
   timeout, cancellation — via `workflow.NewDisconnectedContext`, and verified by
   an independent `List` rather than trusting the destroy call.
3. **Cleanup precedes finalization**, so `run.json` always contains the cleanup
   result. A manifest reporting "cleanup never attempted" would not be auditable.
4. **Retries are typed.** Deterministic operations get `MaximumAttempts: 1`;
   infrastructure gets 5 attempts with backoff; the agent gets **exactly 2**, and
   Temporal's attempt number is written into `agent/result.json` so a retry is
   visible evidence rather than a silent repeat.
5. **The model never decides the result.** `SUCCEEDED` requires every mandatory
   gate to exit zero.

## 4. Result model

States (`internal/factory/model.go`):

| State | Meaning |
| --- | --- |
| `REQUESTED` | Accepted |
| `RUNNING` | In flight |
| `INVALID_REQUEST` | Rejected by policy before any sandbox existed |
| `INFRASTRUCTURE_FAILED` | Cube unreachable, provisioning failed, clone failed |
| `AGENT_FAILED` | The agent exited non-zero or timed out |
| `VERIFICATION_FAILED` | The agent claimed success; a deterministic gate disagreed |
| `SUCCEEDED` | Every mandatory gate passed |
| `CANCELLED` | The workflow was cancelled |

Three outcomes are recorded **separately** and are never collapsed:

```
agent_result        = SUCCESS | FAILED | TIMED_OUT | CANCELLED | ERROR | SKIPPED
verification_result = SUCCESS | FAILED | SKIPPED | ERROR
cleanup_result      = SUCCESS | FAILED | ERROR | SKIPPED
```

The canonical example the design exists to express:

```json
{ "agent_result": "SUCCESS",
  "verification_result": "FAILED",
  "factory_result": "VERIFICATION_FAILED" }
```

## 5. Security boundaries

| Boundary | Enforcement |
| --- | --- |
| Task text | Untrusted. Delivered to the agent via a **stdin file redirect**; it never becomes part of a command string. |
| Repository URL | Operator allowlist of prefixes. `file://`, bare paths, and URLs with embedded credentials are rejected. |
| Revision | Required. Passed as a structured argv element, with `--end-of-options` for option-like values. |
| Agent executable | Operator `[harnesses.<name>]` configuration. A caller names a harness; it can never supply an executable or argument. |
| Verification | Structured `argv` only. There is no shell-string form. |
| Sandbox environment | Only variables named in `pass_env` are forwarded. Everything else on the host is dropped. |
| Cube management credentials | Never placed in a sandbox. The factory never requests an internal-CIDR allowlist entry. |
| Artifact paths | Validated against traversal; run IDs must be plain path segments. |
| Evidence | Redacted on write: registered secret values plus credential-shaped patterns (bearer tokens, `sk-`, `ghp_`, `AKIA`, URL userinfo, `*_PASSWORD=`). |
| Temporal | Bound to `127.0.0.1` only. It is never publicly exposed. |

## 6. Evidence model

Per run, under `<data_dir>/runs/<run-id>/`:

```
task.json                 the request as accepted, with task_hash
baseline.json             kind, source, requested revision, resolved SHA, branch, status
git-before.txt            sha, branch, git status --porcelain
run.json                  the manifest (see below)
changes.patch             the deliverable
git-after.txt             post-run sha and status
cleanup.json              destroy outcome, verification, remaining sandbox count
agent/prompt.txt          exactly what the agent was asked
agent/stdout.log          redacted
agent/stderr.log          redacted
agent/result.json         harness, command line, exit code, timings, attempt
verification/result.json  per-gate argv, timings, exit codes, pass/fail
verification/*.stdout     redacted per-gate output
verification/*.stderr
```

`run.json` records identity, input, environment, timing, the three separate
outcomes, and artifact pointers. It also carries Phase 2 extension fields
(`model`, `model_provider`, `tokens_in`, `tokens_out`, `inference_cost`,
`snapshot_id`, `clone_parent`, `candidate_id`, `strategy`, `evaluator_score`,
`review_findings`, `human_result`) so adding speculative execution does not
require rewriting the manifest.

## 7. Observability

OpenTelemetry, deliberately small. When `otel_endpoint` is empty the factory uses
no-op providers, so instrumented code paths are identical with or without a
collector — instrumentation cannot change behaviour between environments.

Trace shape, with all I/O inside activities:

```
factory.run
  validate
  cube.create
  sandbox.prepare
  repo.prepare
  agent.run
  verify.<profile>
  artifacts.collect
  cube.destroy
```

Attributes: `factory.run.id`, `factory.workflow.id`, `repository.name`,
`repository.revision`, `agent.harness`, `sandbox.provider`, `sandbox.template`,
`sandbox.id`, `verification.result`, `factory.result`. No credential is ever an
attribute.

Metrics: `factory_runs_total`, `factory_runs_success_total`,
`factory_runs_failed_total`, `factory_run_duration_seconds`,
`sandbox_create_duration_seconds`, `agent_duration_seconds`,
`verification_duration_seconds`, `sandbox_leaks_total`.

## 8. Phase 2 seams

Present now, unimplemented:

| Seam | Where | Phase 2 use |
| --- | --- | --- |
| `sandbox.Capabilities{Snapshot,Clone,Rollback,CloneMultiple}` | `internal/sandbox/types.go` | Detect support rather than assume it |
| `sandbox.Snapshotter` | `internal/sandbox/types.go` | Snapshot S0, clone N ways |
| `CandidateID`, `Strategy`, `CloneParent`, `SnapshotID` | `RunManifest` | Candidate identity and lineage |
| `EvaluatorScore`, `ReviewFindings` | `RunManifest` | Deterministic ranking, review stage |
| `ProviderFiles` | `HarnessConfig` | Per-harness credentials for Codex / Claude |
| `Network{AllowOut,DenyOut,AllowInternet}` | `sandbox.Spec` | Egress allowlist policy |
| Activities are already independent | `internal/factory/activities.go` | Fan-out in the workflow, not inside an agent |

The critical Phase 2 rule is already respected: **the factory owns parallelism
and infrastructure**. Agents operate only inside the Cube assigned to them and
never manage Cube infrastructure themselves.

## 9. Explicit non-goals for Phase 1

Not built, and not needed to satisfy the vertical slice:

React UI · Backstage · Coder · Kubernetes · OIDC · RBAC · Vault · OPA · Jira ·
GitLab / Azure DevOps integration · automatic PR creation · automatic merging ·
RAG · long-term memory · billing · model router · dashboards · snapshot fan-out ·
candidate ranking · reviewer agents · repair loops.
