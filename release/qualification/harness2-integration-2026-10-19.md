# Harness2 integration candidate: 2026-10-19

Status: implemented candidate; production adaptation disabled and unqualified.
Base: `d41535d86a13e55fe2357d9e0a108566d68ce1b9` on `master`.
Upstream method pin: `1cfe254e8d25b15f37048c3730134686abe60335`.

## Local validation

Project tests execute inside disposable local Cube VMs, after host compilation.
The retained [regression receipt](evidence/harness2-20261019/cube-regression.json)
binds the source archive and race-enabled test binary hashes, complete test logs,
template, time and verified cleanup. `go vet ./...` also passes.
The CI-pinned Gitleaks 8.30.1 scanner also passes inside Cube; its
[receipt](evidence/harness2-20261019/secret-scan.json) retains the scanner hash
and verified cleanup. Source checksums use separate path/hash fields so filenames
containing `credential` are not misclassified as credential assignments.

Coverage includes instructional bundle rejection and corruption, independent
signed approval and renewal, cross-scope/runtime/route rejection, deterministic
qualification and negative quality cases, concurrent cross-process reservations,
unknown streaming usage and restart accounting, four-solver orchestration,
review authority, ordinary task failures, comparison partition contamination,
CLI selectors, and release evidence bindings. Synthetic quality inputs and test
signing keys are fixtures; they do not qualify a real profile.

The actual pinned OpenCode2 executable reads the profile and skill through its
tool loop, with requests passing through the bounded gateway to a deterministic
local TLS provider. This establishes protocol and component loading compatibility
for that binary; it does not establish model-backed quality or billing accuracy.

[Temporal/Cube acceptance evidence](evidence/harness2-20261019/acceptance.json)
records the following real local workflows, using a copied synthetic repository:

| Case | Result |
| --- | --- |
| Valid change, explicit test approval | `SUCCEEDED`, mandatory gates pass |
| Valid change, explicit test rejection | `REJECTED`, despite passing gates |
| Zero-exit no-op agent | `VERIFICATION_FAILED`, unchanged mandatory test rejects |
| Improvement with inconclusive boundary probe | Baseline `EGRESS_UNVERIFIED`; parent fails; eight accounts revoked, zero model requests |

Every completed case verifies VM cleanup. The first two acceptance attempts
omitted the recorded Cube version from the isolated test configuration and
detected reachable internet despite a requested deny. ESF rejected both and
cleaned them up. Setting the already-recorded `cube.version = "0.7.3"` activates
the existing create compatibility path; no primary checkout or stack was changed.

With that correction the probes report `UNKNOWN`, which does not prove denial.
The new improvement controller rejects this evidence before inference and retains
the circuit/failure receipts. Existing ordinary conformance runs retain their
previous behavior. Test approvals here are orchestration fixtures, not independent
review or release approval.

## Production gates still outstanding

- Conclusive real-provider network/credential isolation evidence on the release
  template, authoritative subscription pricing and metering, and model-backed
  component activation and mandatory-gate rejection.
- An owned, versioned corpus with at least 100 untouched qualification tasks,
  three repeats, lineage separation, independent acceptance, and comparisons
  against both single and repeated baseline attempts. No ESF quality gain has
  been measured and no reusable production profile is promoted.
- Worker crash/cancellation/replay recovery on the real deployment, shared-store
  restore, VM and Kubernetes qualification, security/load checks and 24-hour soak.
- Independent code/quality approval, exact-head hosted CI and approved pilot
  policy. One-percent enrollment precedes a reviewed seven-day/100-task expansion.

The release inventory remains unqualified. This record cannot authorize rollout.
See [operator instructions](../../docs/harness-improvement.md) for promotion,
budgeting, policy activation, evidence requirements and rollback.
