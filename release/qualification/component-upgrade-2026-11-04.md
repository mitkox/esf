# Component upgrade validation, 2026-11-04

The candidate starts from `origin/master` `482d963a` (merge of the v0.7.2
release branch) and refreshes the remaining behind-latest component and
dependency pins, corrects two stale documentation pins, rebuilds the local Cube
template chain and validates the result end to end. Upstream metadata is
retained in [the audit](evidence/component-upgrade-2026-11-04/upstream.json);
build and runtime evidence is retained beside this record.

| Component | Previous | Candidate |
|---|---|---|
| ESF release | v0.7.2 | v0.7.3 |
| Claude Code | 2.1.317 | 2.1.318 |
| Temporal admin tools | 1.32.1 | 1.32.2 |
| `grpc-gateway/v2` (indirect) | v2.32.0 | v2.33.0 |
| `go.yaml.in/yaml/v3` (indirect) | v3.0.5 | v3.0.6 |
| Python `filelock` | 4.2.7 | 4.2.9 |
| Python `typing-inspection` | 0.4.4 | 0.4.5 |
| Console `lucide-react` | 1.67.0 | 1.68.0 |
| CI `astral-sh/setup-uv` | v10.3.2 | v10.3.3 |
| Cube template Ubuntu security pins | none | openssl/libssl3 3.0.2-0ubuntu1.30, libbrotli1 1.0.9-2ubuntu0.1 |

OpenCode 2.2.5, Codex 0.166.0, Unreal 0.3.1, Pi 1.5.0, CubeSandbox 0.7.3
(server and Go SDK), Temporal server 1.32.2 / UI 2.58.0 / Go SDK 1.50.0 / Go API
1.64.1, grpc 1.85.0, OpenTelemetry 1.47.0, protobuf 1.36.12, `golang.org/x/net`
0.62.0, PostgreSQL 18.6, dspy 3.4.1, Go 1.27.3, Node 26.13.0, Python 3.15.2 /
3.14.10, uv 0.13.7, kubectl 1.37.2, Helm 4.3.1, govulncheck 1.9.0 and gitleaks
8.30.1 remain at their current releases. Four Python packages remain
constraint-blocked (`litellm`, `openai`, `importlib-metadata`,
`python-dateutil`); `diskcache 5.6.3` retains an unfixed upstream advisory, its
vulnerable path is disabled in both DSPY entry points, and no upgrade exists.
The adapted harness2 provenance pin (`1cfe254e`) is unchanged: it records
adapted role instructions with no upstream runtime, and no upstream release
exists to move to.

## Documentation pin corrections

The private turn-executor guide and example profile still pinned Claude Code
2.1.272 from an earlier qualification; they now pin 2.1.318 with its binary
SHA-256 (`deploy/turn-executor/README.md`, `profiles.example.json`,
`docs/sf-turn-executor.md`; the 2.1.272 qualification narrative is retained as
history). `docs/opencode-go.md` carried a stale OpenCode binary digest; it now
records the shipped 2.2.5 payload hash `8d060e1d…`.

## Cube validation

Project execution used disposable local Cube microVMs. The new template is
`tpl-4ae712b3f614434a90959a81` (alias `esf-v073-native-20261104`), image
`localhost:32001/esf-native@sha256:1fdb1c00…1fb7`, built from the tracked recipe
plus the operator native-CLI overlay and the Pi/Node overlay; every payload
digest was verified by `factory agents verify` and `factory doctor`.
Evidence: `evidence/component-upgrade-2026-11-04/template-build.json`.

One `bin/cube-test` invocation in a disposable sandbox
(`287c2bca98c0496ead8aae60a330da45`) executed:

- 17 compiled test binaries, **731 tests passed**, every binary exit 0 (one
  intentional helper skip);
- both release checks, exit 0 (release inventory pins; qualification boundary
  tests, `Ran 3 tests ... OK`);
- gitleaks 8.30.1 over the packed tracked tree, **no leaks found**;
- verified cleanup: `remaining_owned=0`, sandbox destroyed before exit.

Evidence: `evidence/component-upgrade-2026-11-04/cube-test.json` and
`cube-test-summary.json`. The source archive packed the tracked working tree
including uncommitted candidate content (sha256 `e943ac37…`); 18 intentionally
deleted console `dist` assets are absent from the archive and 18 regenerated
assets are untracked build artifacts outside the archive, though they are
compiled into the console test binary. The Python release checks used a
portable CPython 3.12.16 runtime.

Static checks: `make lint` (gofmt, `go vet`, shellcheck) passed; `npm ci` and
the console unit tests passed 17/17 and rebuilt the tracked `internal/console/web/dist`
deterministically (an independent `npm ci && npm run build` reproduces it
byte-identically); `python3 scripts/verify-release-inventory.py` passed.

## Acceptance

The conformance acceptance run (`run-38608072c1ffedaa837294bd`) passed every
deterministic gate on the new template: verified patch, gate integrity
unmodified, behavior scan clean, cleanup verified with zero remaining
sandboxes, and `factory sandboxes` reported no live sandboxes afterwards. The
egress probe reports `Unknown/ProbeInconclusive` because the local profile
acknowledges open egress; this is not a denial-path failure.

A model-backed OpenCode 2.2.5 acceptance run was attempted
(`run-94a44931a9311bfcb9f47ae7`) but the external model provider did not return
within the validation window; the run was cancelled and its sandbox
(`571bcc84d33a4566b10854a17e2ca2b2`) destroyed. It is not counted as evidence.
Evidence: `evidence/component-upgrade-2026-11-04/acceptance.json`.

`factory doctor` passed (three known hardening warnings), `factory agents
verify` matched all six configured agents and the Pi runtime self-check against
the new template, and `make cube-smoke` passed on the new template
(Ubuntu 22.04.5, node v26.13.0) with zero leaks.

## Security re-validation

- `govulncheck` 1.9.0 over the full module graph and the built binary: no
  findings. OSV (332 queries) and deps.dev corroborate; the only advisory in
  the Python lock is the unfixed `diskcache` Medium, not reachable.
- `npm audit --audit-level=high` and OSV for the three shipped npm graphs: no
  findings.
- `pip-audit` with the exact lock pins and OSV for both interpreter closures:
  only `diskcache`.
- `grype` (DB 2026-11-04T06:20Z) on the pinned images: the `admin-tools`
  bump removes all 2 Critical and 15 High fixed CVEs present in 1.32.1 (1.32.2
  scans clean); the new template base removes the two Ubuntu High findings
  (`CVE-2026-84782` openssl/libssl3, `CVE-2026-111497` libbrotli1) while the
  remaining fixed set is the upstream `envd` Go 1.25.4 binary (2 Critical and
  22 High; no newer Cube base tag exists). Remaining upstream-rebuild gaps are
  the Unreal agent runner built with Go 1.27.0 (8 High; v0.3.1 is latest), the
  Temporal UI 2.58.0 (1 High in `x/net`; latest release), and the dev-only
  PostgreSQL 18.6-alpine image (8 Critical and 17 High in libcurl/brotli;
  upstream digest unchanged and an `apk upgrade` derived image was not
  applied). The untracked `.github/site` tooling carries two High advisories and
  is not shipped.

Evidence: `evidence/component-upgrade-2026-11-04/security-rescan.json`.

## Independent review

An independent read-only review of the candidate diff was collected before
commit. It returned **approve with fixes** with no blocker or high-severity
finding: version consistency, inventory/embedded equality, the recomputed Cube
recipe hash, Claude and admin-tools digests, `setup-uv` SHA resolution, the
Ubuntu apt pins, and the byte-identical console `dist` rebuild were confirmed;
the diff contains no secrets or host-specific paths. The supported findings
were fixed before commit: the dev PostgreSQL image risk is stated with counts
and the missing derived-image patch, this qualification record and its evidence
are committed, the Helm chart README and root README point at the 0.7.3
records, the release notes list the documentation pin corrections and the apt
unpinned-package drift note, and the incomplete model-backed run is not cited
as evidence.

## Remaining qualification

Image/template publication through the release workflows, model-backed
acceptance for the refreshed Claude/Codex/Unreal clients, hosted-provider
checks, the 24-hour VM and Kubernetes soak, the performance matrix, per-image
SBOM/vulnerability qualification, transitive-license review, restore, Temporal
history replay, migration drill, production deployment and eligible human
approval remain separate gates. Local unit and acceptance results do not satisfy
them, and this record is not production approval.
