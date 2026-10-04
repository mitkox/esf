# ESF 0.6.0 review findings and qualification status

Status: candidate implementation; production qualification is incomplete.
The review starts from `master` at
`2bd466256b6ae5ffcca032127a2583f674211358`. Historical 0.5.0 evidence is not
qualification evidence for this candidate. Post-build qualification must bind
the final source and every built asset through the companion manifest described
in [the release guide](release-v0.6.0.md).

## Corrected findings

| ID | Severity | Location | Reproduction and effect | Correction | Validation |
| --- | --- | --- | --- | --- | --- |
| R01 | High | `internal/repository/validate.go`, `internal/factory/resources.go` | A raw prefix accepted adjacent repository paths and lookalike authorities, widening the admission policy. | Compare parsed scheme, hostname, effective port, and a canonical path boundary. Reject query, fragment, userinfo, and unsafe paths. | Focused admission boundary cases and existing repository race tests. |
| R02 | High | `internal/factory/resources.go` | A closed named policy that was not selected left a run inheriting the provider's open default. | Resolve an unselected policy to explicit denied internet when open egress has not been acknowledged. | Focused resolution regression and existing scope/policy race tests. |
| R03 | High | `internal/artifacts/store.go` | A parent or staging-directory symlink could direct publication or upload outside the store. Conflicting retries were silently accepted. | Anchor staging, opening, linking, and directory creation with `os.Root`; verify existing content on retry and sync publication directories. | Extend the existing bounded immutable publication test with conflicting content and external symlinks; race tests. |
| R04 | High | `internal/factoryartifacts/store.go` | Reads and writes could follow a run-directory or nested parent symlink outside the artifact root. | Anchor operations to the storage root; atomically rename synced temporary files and sync ancestor directories. | Extend the existing traversal test; existing read/write concurrency race tests. |
| R05 | High | `internal/managedworker/client.go`, `artifacts.go` | Redirects could forward worker/lease credentials or mutate the endpoint. Shared input paths could follow an external output symlink. | Reject credentialed endpoint userinfo and redirects; materialize inputs under the job root. | Credentialed redirect regression and extended input filesystem failure test; existing managed-worker race suite. |
| R06 | Medium | `internal/factory/control.go`, `cmd/factory/hardening.go` | Incident halt decoded encrypted visibility memos with the plaintext converter, preventing correct filtering. | Use the configured Temporal data converter and fail on undecodable memo evidence. | Extend existing pagination/halt coverage with encrypted memos. |
| R07 | High | `internal/factory/egress_probe.go`, `workflow.go` | DNS, TLS, or connection errors were recorded as blocked networking, producing a false successful assurance condition. | Report undecided observations and `EgressVerified: Unknown` when the probe cannot establish the boundary. | Execute the probe with a failed DNS command in the existing probe test; live runs retain inconclusive observations. |
| R08 | High | `internal/factory/change.go`, `cmd/factory/resources.go` | Concurrent completions could lose activations despite atomic file replacement. A renamed change record lacked directory durability. | Lock each change across processes during read/modify/write, preserve activations during abandonment, and sync the renamed directory entry. | Concurrent completions regression across independent stores, existing change/idempotency tests and race suite. |
| R09 | High | `deploy/helm/esf/templates/factory.yaml`, `cmd/factory/credentials.go` | Projected Kubernetes Secrets use symlinks and group-readable permissions rejected by strict factory credential readers. | Restricted init container copies bounded credentials into owner-only regular files on a memory-backed volume; worker reads a private snapshot. | Projected-file, escaping-link, oversize and overwrite cases; Helm rendering with credentials. Live Kubernetes qualification remains required. |
| R10 | Medium | `internal/factory/hardening_evidence.go`, `worker.go` | Webhook token loading bypassed existing credential bounds/permissions and omitted the token from redaction registration. | Reuse the bounded private credential reader and register the token with the redactor. | Existing credential and hardening race coverage. |
| R11 | High | `scripts/release_qualification.py`, release workflows | Stale `main` references and 0.5.0 assumptions obstructed candidate delivery; no companion verifier bound post-build evidence to final assets. | Use `master`, inventory-derived versions, exact source/artifact bindings, complete gate evidence, quantitative acceptance limits, and an attested qualification workflow. | One table-driven qualification boundary test plus workflow syntax, inventory consistency and release archive checks. Production evidence remains pending. |

## Validation recorded during implementation

Project checks run in dedicated Cube microVMs. The host performs builds and
factory/operator actions. Existing Go race coverage ran across the repository;
affected packages are repeated only for fixes or failed checks. Frontend tests
(40), bundle freshness, intake tests (8), brief-lab tests (7), eval tests (71),
and issue-triage tests passed. Go vet, formatting, shell syntax/ShellCheck,
actionlint, redacted Gitleaks, Helm rendering, and builds passed during review.
The added regressions cover security or evidence boundaries absent from the
existing suite; most corrections extend existing tests.

Current Go vulnerability analysis and npm bulk advisory checks found no
vulnerabilities in the checked graph and locked frontend/OpenCode packages.
The Python lock has one DiskCache unsafe-pickle finding represented by
`GHSA-w8v5-vhqr-4h9v` and `PYSEC-2026-2447`. Both shipped optional-tool entry
points call `dspy.configure_cache(enable_disk_cache=False,
memory_max_entries=1024)` before model use and load promoted programs as JSON.
The vulnerable persistent deserialization path is disabled in these entry
points. Retain this scoped exploitability assessment; it does not clear use of
DiskCache elsewhere or image/package scans. Advisory details:
[GitHub advisory](https://github.com/advisories/GHSA-w8v5-vhqr-4h9v).

An isolated configuration with Temporal TLS, encrypted payloads, closed egress,
and local disk storage passes production doctor. Development live scenarios
record success, rejected admission, agent failure, verification failure,
agent timeout, cancellation, review approval/rejection/timeout, and rework
lineage with cleanup. Admission rejection allocates no sandbox. Review timeout
preserves the 0.5.0 contract: verification may succeed while `human_result` is
`timeout`; it must never be treated as human approval. These are development
results and must be repeated against the exact qualified candidate inputs.

## Remaining production gates

The local model is alive and its `/responses` endpoint responds. The current
Cube-to-provider network path still fails. Host CoreDNS health does not prove
guest DNS reachability. The per-run public/metadata probes are inconclusive.
Neither failed connectivity nor conformance substitutes for model-backed
OpenCode/Unreal acceptance or measured egress enforcement.

The following evidence remains required before production qualification:

- Exact candidate VM and MicroK8s deployment, actual CNI/permission enforcement,
  readiness/liveness behavior, restart and shutdown, persistent storage,
  consistent backup/restore, and drained 0.5.0 upgrade and rollback.
- Retained 0.5.0 Temporal history replay, including review and removed-QMS
  compatibility markers; encryption round trips alone are insufficient.
- Real pinned OpenCode and Unreal acceptance, successful provider protocol/tool
  use from Cube, metadata/egress enforcement, encrypted-history leakage checks,
  gate tampering and artifact/output limits on the built inputs.
- Live worker interruption and injected cleanup failure with reconciliation,
  plus retained Cube suspend/resume evidence for the exact candidate template.
- Exact per-platform image digests, SBOM and vulnerability assessments,
  transitive license review, provenance, and two reproducible archive builds.
- Equivalent 0.5.0/0.6.0 load measurements: 30 batches at concurrency 1, 4 and 8
  on each target, no incorrect results or leaks, and no greater than 20% p95 or
  peak memory regression; 24 real hours of soak on each target.
- Required Linux/macOS CI on the final source and independent PR review.

Lower-risk follow-ups include bounding GitHub CLI response buffering and
validating deployment probes against an unresponsive worker, rather than only
a fresh diagnostic process. Probe behavior must be assessed in live deployment
qualification before it can satisfy the deployment gate.

No merge, final tag, or published 0.6.0 release is part of this delivery. Raw
logs, private configurations, credential values and host identifiers stay out
of public findings and release evidence. All inventory qualification gates stay
pending until the matching companion evidence passes review and verification.
