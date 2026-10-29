# OpenCyber Cube qualification and bounded private pilot

Date: 2026-10-27. Issue: [#50](https://github.com/mitkox/esf/issues/50).

**Decision: GO for the manually initiated synthetic profile on the tested local
stack.** The replacement endpoint route, selected pinned native backend,
three before/after pairs and concrete factory repair workflow passed their
reported checks. General activation remains disabled by default. This is
local qualification and an integration handoff, not human approval or a
recommendation to make the assessment mandatory.

## Identities and execution boundary

Implementation started from `ba829056ab728b8e002dddcb702edc0be5b19a1d` in an
isolated checkout. The dirty primary checkout was preserved. Transferred source
archives identify the tested working-tree bytes; the base revision alone does
not identify the tested implementation.

| Input | Identity |
|---|---|
| OpenCyber source | `09692d89e3cb9dce3506cd4e6fc92c9e4c3c16b6` |
| Source archive SHA-256 | `1c0fdac511147b99cc4f4285305c16962e0f7a46e192c7e5d99fecb9150fa0e7` |
| OCI index digest | `sha256:be24e270c130f3c99ac5989828bd85e98a8bdc07d3991f15d744ad412517b4ed` |
| Cube rootfs SHA-256 | `d603978574994277a16d3f00be0ac73db53f2b79d3122e194985b42a21f80f12` |
| READY assessment template | Exact local ID retained in private operator qualification |
| Runtime inventory SHA-256 | `435309aba70f05c61cec853b53d11ada41ef5a195ad8bad23d15d36ac3acf266` |
| Recorded catalog SHA-256 | `6ff0d628d1e93da78345b05ad63cdcd27adade55de29938ead97c4a7560b6660` |
| Python / MCP / requests | 3.13.16 / 2.3.0 / 2.34.2 |
| Cube / envd | 0.7.3 / 0.5.13 |

Host execution built trusted binaries and drove Cube/Temporal orchestration.
Application code, OpenCyber, Go tests, vet and Python tests ran inside Cube.
Installation happened in Cube; OCI packaging used COPY without executing the
application on the host. Neither target nor assessor received Cube management,
SCM write or release credentials, a Docker socket, or a host filesystem mount.

## Endpoint and lifecycle results

The original shared preview-proxy IP policy was **NO-GO**: the assessor reached
another live target port. That historical failure remains valid. Its replacement
does not widen the shared allowance: a dedicated address and firewall accept
only TCP 9445, and an HTTPS broker binds the target instance, service port,
authority, per-attempt capability and exact GET paths.

| Observation | Result |
|---|---|
| Approved HTTPS target, instance and correlation | Pass before and after denial checks |
| Alternate live TCP port | Reachable control, then denied |
| UDP on endpoint port | Echo control, then no echo and exact owned reject-counter increment |
| Forged authority, encoded/path/absolute-URI escape, POST | HTTP 403 |
| Missing endpoint capability / application token | HTTP 403 / 401 |
| Redirect escape | Refused; no redirect following |
| Direct preview, management and metadata destinations | Denied from assessor |
| Target-origin preview, management and endpoint access | Denied, with same-tuple reachable controls |
| Target local write / assessor filesystem | Assessor sentinel hash unchanged |
| Normal completion | Owned VM/address/guard cleanup verified |
| Target startup failure | Incomplete; cleanup verified; explicit fresh recovery passed |
| Context timeout / cancellation | Original cause retained; cleanup verified; explicit fresh recovery passed |
| Controller SIGKILL after live broker request | Receipt-based reconciliation removed owned VMs and guard; repeated cleanup idempotent |
| Lost create acknowledgment | No retry; incomplete uncertainty retained; independently observed allocated ID removed |

TCP/UDP denial used the same live listener and correlation as its positive
control. Missing ICMP feedback was not treated as proof. The root helper is
UID-authenticated and accepts only owned guard operations; normal shutdown and
lease expiry clean its state. Root-helper SIGKILL/workstation-crash recovery
was not qualified and still needs independent kernel reconciliation.

Private route receipts intentionally keep overall `decision=incomplete` and
`integration_enabled=false`: they establish the route sub-gate. This report
combines their observations with separately retained native, lifecycle and
factory evidence. It does not relabel a route probe as a full assessment.

## Native assertions and measurements

Three vulnerable/replacement pairs used synthetic user A, A's legitimate
resource and B's forbidden resource. All vulnerable runs confirmed the seeded
disclosure while legitimate access passed. All replacements denied the same
unauthorized assertion, preserved legitimate access, and supplied the matching
denial correlation in the actual bounded timeline report. Exports verified
against hashes held outside target-writable state before teardown.

Each assessment made five target requests, including readiness/log-completeness
reads: 30 requests across six assessments. Observed assessment time was
2.589–2.619 seconds, median 2.616 seconds. Candidate-staging time is separate.
Configured ceilings were 15 minutes, 100 target requests, 16 MiB log input and
16 MiB selected evidence, with zero automatic repair attempts.

The native runtime baseline occupied 204 MiB of userspace and used a 2000 MiB
VM allocation. Per-attempt peak memory, representative marginal cost and
human triage effort were not measured. There are no additional unseeded
confirmed findings or representative delivery samples supporting expansion.

## Concrete factory workflow

The final operator-facing pair used a separate task queue/storage, an exact
committed synthetic fixture, deterministic Cube agents, and the final pinned
assessor driver. Intake/model calls were disabled. Repair was a separately
initiated run with a parent reference; the assessor did not repair its target.

- Before factory run: functional gates passed; tenant-isolation
  failed; expected absence of denial telemetry remained unobserved. The summary
  preserves **failed** independently of incomplete assertion coverage.
- Repaired factory run: parent references the before run;
  replacement candidate digest differs; all four security assertions passed.
- Both retain unchanged gate hashes, independent review timeout, no approval
  signal and verified owned VM cleanup. The builder's general egress probe
  remained inconclusive; this does not replace the assessor route proof above.

Real Temporal cancellation tests withheld executor/capture cleanup acknowledgment
and proved that workflow completion waited for it. They passed in 31.13 and
31.11 seconds. SDK replay passed for an actual old 107-event history and new
101-event disabled / 115-event enabled histories. The new enabled replay used
a fake builder and explicit unavailable executor; the real factory pair above
separately validates the concrete executor.

## Verification and evidence index

The full Go suite, `go vet ./...`, eight fixture tests and five probe tests
passed inside Cube using Go 1.27.3/Python 3.10.12. Final review changes received
affected-package rechecks. Private receipts retain commands, snapshots, run IDs,
candidate/evidence hashes and observed cleanup. No host test result is implied.

Publication checks also passed the complete `just check` recipe in a fresh
Cube: six issue-triage tests, 40 frontend tests and its production build,
formatting, 71 evaluation tests, three script tests, `go vet ./...`,
`go test -race ./...` and `go build ./...`. This used Go 1.27.3,
Node 26.12.1 and Python 3.15.2. Initial attempts exhausted VM storage/RAM;
the successful run reclaimed completed frontend tooling, moved checksum-verified
Go cache objects from tmpfs to disk, and used serial Go execution,
`GOMEMLIMIT=128MiB` and `GORACE=history_size=0`. Project checks were unchanged.
All 17 tracked frontend build files matched their retained hashes.

Darwin arm64 `go build ./...` and cross-compilation of the route and executor
tests passed. Tests were not executed on macOS locally. The assessment backend
requires Linux; unsupported hosts reject before journal creation or Cube API
calls. Pinned Gitleaks 8.30.1 found no secrets in the publication source.
The public runtime recipe omits a private provenance label from the qualified
image; rebuilding it changes the image identity and requires fresh pins and
qualification.

| Retained proof | SHA-256 or private receipt name |
|---|---|
| Full-suite source snapshot | `568719b7c462d37732f3ea4788ce9eba330247298d41ae103ae6dd6620a53442` |
| Strict-normalization affected-package snapshot | `166108b8e07035b7cfef64ea88ff493d7b66910970d35380ab0679d1089bf833` |
| Old replay history | `2bd89cc14a6ee46787973d3b1d98504ba33cb276a82df34edc334aa47faab90d` |
| New disabled replay history | `fbed74ca7db9bf0747a5356ab58c99fcd6f981c0d8781bee6d61f5e9f7f83393` |
| New enabled replay history | `5841f299b64ca8f94f1125c4a17eff5e5dd562fc81d7121da5c6caf4dc4e49bc` |
| Final assessor driver | `fbc58cf76055a16f0b27e0d35bf11248d022b4437906ce6a243213562e366d3f` |
| Final factory activity source overlay | `2ff20fe075d29dcdb06bd5fa3190852c5ed03ed579dbcc3504cc6d7790cdd777` |
| Final factory regression source overlay | `8c9cc6e070fe64430d3d0c06a5415c0c12a68f3847dfcb192f310d44c90ca25f` |
| Route and target isolation | `route-isolation-v5.json` and adjacent probe artifact |
| Repeated native assessments | `pilot-three-pairs.json` and its private evidence directory |
| Startup/cancel/timeout recovery | `pilot-*-final.json` |
| Interruption/lost acknowledgment | `interruption-checkpoint.json`, `route-interrupted-v2.json`, `lost-ack-observation.json` |
| Actual factory pair | `factory-pilot/` receipt, manifests, gate evidence and patches |

The actual factory receipt SHA-256 is
`d053c760b0fc3666202d153b0997571065b659d1c7b88d56e11cbb4b9dc61ab9`.
The parent independently rechecked all 14 selected evidence files and ordinary
run artifacts. The final validation receipt SHA-256 is
`7876c02a6fdaeb4ec1681837e37ca143d755861d8eb21df3d611331b8e7b7376`.
Final factory tests and vet passed after the confirmed-failure summary fix.
After the validator was destroyed, `factory sandboxes` reported no live
sandboxes. The temporary guard acknowledged shutdown, its socket disappeared,
and loopback retained no TEST-NET aliases.

Independent source review completed with no remaining actionable finding after
fixes for bounded transfer, Temporal cancellation, fault causality, strict log
framing and preserving confirmed failures. Review is separate from eligible
human approval. Compound Engineering retained a [workflow learning](../../docs/solutions/workflow-issues/qualified-native-contracts-before-factory-integration.md)
and the executable qualification/pilot tools rather than broadening permissions.

## Handoff and limits

Use the [endpoint runbook](../../deploy/cube/README.security.md),
[native pilot](../../tools/cube-security-pilot/README.md) and
[factory configuration guide](../../docs/security-assessment.md). Profiles are
explicitly named, scope-granted and hash-pinned; the default remains disabled.
The template artifact is retained, but temporary guard service activation must
be repeated by an operator for another local pilot.

Optional AI was disabled; no model inference or provider substitution is claimed.
The broader toolkit self-test was degraded by missing optional external tools
and browser engines; only the selected native tools were qualified. Full
failure permutations, representative delivery economics, workstation-crash
recovery, CI, human approval, publication, merge, deployment and production
acceptance remain separate. Keep this optional until representative evidence
justifies a particular broader profile.
