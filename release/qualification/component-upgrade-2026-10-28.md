# Component upgrade validation, 2026-10-28

The candidate starts from GitHub master
`d8009eba4e9834bbd7d0f385d098e669c6670632` and refreshes every maintained
component and dependency pin to its current stable release. Upstream metadata is
retained in [the audit](evidence/component-upgrade-2026-10-28-upstream.json);
build and runtime evidence is retained beside this record.

| Component | Previous | Candidate |
|---|---|---|
| ESF release | v0.7.0 | v0.7.1 |
| OpenCode | 2.1.4 | 2.2.0 |
| Claude Code | 2.1.300 deployed / 2.1.311 inventory | 2.1.312 |
| Codex CLI | 0.162.1 deployed / 0.164.0 inventory | 0.165.0 |
| Python (optional tooling) | 3.14.7 | 3.14.10 |
| grpc | 1.84.0 | 1.85.0 |
| golang.org/x/net | 0.61.0 | 0.62.0 |
| kubectl qualification | 1.37.1 | 1.37.2 |
| Lucide React | 1.57.0 | 1.63.0 |
| Vite | 8.3.4 | 8.3.5 |
| Python boto3 / botocore | 1.43.119 | 1.43.120 |
| Python pydantic-settings | 2.16.1 | 2.16.2 |
| node 26.12.1-slim digest | `36793bcb…` | `94816674…` |
| debian trixie-slim digest | `62bd423a…` | `89c1ea91…` |
| golang 1.27.3 digest | `9048d5e7…` | `b7602092…` |
| python 3.15.2-slim digest | `197f6622…` | `c4158543…` |

Go 1.27.3, Node 26.12.1, uv 0.13.6, Helm 4.3.1, PostgreSQL 18.6, Unreal 0.3.1,
Pi 1.4.0, CubeSandbox 0.7.3 (server and Go SDK), Temporal 1.32.1 / UI 2.57.0 /
Go SDK 1.50.0 / API 1.64.0, dspy 3.4.1, certifi 2026.10.23 and filelock 4.2.4
remain at their current releases. Four Python packages remain
constraint-blocked (`litellm`, `openai` 3.x, `importlib-metadata` 10.x,
`python-dateutil` 3.x); `litellm ≥1.104` conflicts with the root
`filelock>=4.2.1` security minimum. `diskcache 5.6.3` retains an unfixed
upstream advisory with no upgrade path.

## Cube validation

Project execution used disposable local Cube microVMs. The primary validation
sandbox was `45399f5079cf470a86e720ce6c4bd606` in template
`tpl-acd55ad29bc04f08b1d3db44`; the acceptance sandbox was
`bd21a58aa09a4b88a8963ee4d6cfb85b`.

- In-Cube Go tests: 14 compiled test binaries, 689 tests passed, every run exit
  0, including the native-client canary after its stale OpenCode digest fixture
  was corrected from `ff170110…` (v2.0.29) to `87a4d6ac…` (v2.2.0).
- In-Cube release checks: `scripts/verify-release-inventory.py` and
  `scripts/tests` passed against the staged release tree.
- Static checks: `go vet`, `gofmt`, `govulncheck` (no reachable
  vulnerabilities), release and Pi inventory verification, and the CycloneDX
  SBOM generator all passed.
- Console: 17/17 frontend tests, production build byte-identical to the
  committed tracked `dist`, and `npm audit --audit-level=high` reported zero
  vulnerabilities for the console, OpenCode and Pi npm graphs.
- Acceptance: `PHASE 1 ACCEPTANCE TEST: PASSED` for run
  `run-86427a478f705eb4dfd0a9aa` on the new template — all gates passed, a
  verified patch was produced, and cleanup reported zero remaining sandboxes.
  The egress probe reported `Unknown/ProbeInconclusive` because the local
  profile acknowledges open egress; it is not a denial-path failure.
- `factory agents verify` matched all six template binaries, `factory doctor`
  passed with three known hardening warnings, and `make cube-smoke` passed
  against the new template.
- Secret scan: the `git ls-files` release tree scanned clean. The
  working-directory-wide scanner command reports only gitignored `.factory/`
  run artifacts and `.rtk/` caches, which cannot be part of the release.
- Owned sandboxes were destroyed; final `factory sandboxes` is empty and the
  Cube API reports zero sandboxes. The new template remains READY.

## Local system

The local intake runtime was rebuilt from the refreshed lock on CPython 3.14.10
at `.factory/runtimes/intake-20261028-3.14.10` (dspy 3.4.1, certifi 2026.10.23,
filelock 4.2.4; `uv pip check` clean). The operator template was rebuilt through
the base → native CLI overlay → Pi/Node overlay chain and registered as
`esf-v071-native-20261028`, image
`localhost:32000/esf-native@sha256:64c54a3ef330f8e179c7b417fa4d5df78d0802afcef9d4663f418eaab9287793`;
every payload digest matched its pin. `factory.toml` was repointed to the new
template, harness digests and intake runtime, and `factory agents verify`
confirmed the result. Historical templates and release records were retained.

## Remaining qualification

Image/template publication through the release workflows, model-backed
acceptance for the refreshed native CLIs, hosted-provider checks, the 24-hour VM
and Kubernetes soak, the performance matrix, per-image SBOM/vulnerability
qualification, transitive-license review, restore, replay, migration drill,
production deployment and eligible human approval remain separate gates. Local
unit and acceptance results do not satisfy them, and this record is not
production approval.
