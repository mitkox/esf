# Component upgrade validation, 2026-11-03

The candidate starts from local `master` HEAD
`4639f046267417bbe5f74ace18af3fbafdf2163d` and refreshes every maintained
component, toolchain and dependency pin to its current stable release, upgrades
the matching locally installed components, rebuilds the local Cube template
chain, and validates the result end to end. Upstream metadata is retained in
[the audit](evidence/component-upgrade-2026-11-03-upstream.json); build and
runtime evidence is retained beside this record.

| Component | Previous | Candidate |
|---|---|---|
| ESF release | v0.7.1 | v0.7.2 |
| OpenCode | 2.2.0 | 2.2.5 |
| Claude Code | 2.1.312 | 2.1.317 |
| Codex CLI | 0.165.0 | 0.166.0 |
| Pi Durable / Pi AI / Chord | 1.4.0 | 1.5.0 |
| Node.js | 26.12.1 | 26.13.0 |
| uv | 0.13.6 | 0.13.7 |
| Temporal server | 1.32.1 | 1.32.2 |
| Temporal UI | 2.57.0 | 2.58.0 |
| Temporal Go API | 1.64.0 | 1.64.1 |
| Python boto3 / botocore | 1.43.120 | 1.43.123 |
| Python filelock | 4.2.4 | 4.2.7 |
| Lucide React | 1.63.0 | 1.67.0 |
| Tailwind CSS / Vite plugin | 4.3.3 | 4.3.4 |
| Vite React plugin | 6.1.2 | 6.2.1 |
| golang 1.27.3 image digest | `b7602092…` | `2920bb12…` |
| node 26.13.0-slim image digest | `94816674…` (26.12.1) | `e4db4c91…` |
| python 3.15.2-slim image digest | `c4158543…` | `8c13a3f3…` |
| debian trixie-slim digest | `89c1ea91…` | `f6b08b09…` |
| distroless static-debian13 digest | `15f86375…` | `44881035…` |
| temporal server / UI digests | `acecbab5…` / `d944f959…` | `7bf69188…` / `d36185e8…` |

Go 1.27.3, Python 3.15.2 (optional tooling) / 3.14.10 (`.python-version` and the
local intake runtime), Unreal 0.3.1, PostgreSQL 18.6-alpine, Temporal admin
tools 1.32.1 (upstream publishes no 1.32.2 tag), Temporal Go SDK 1.50.0,
CubeSandbox 0.7.3 (server and Go SDK, the latest published stable release),
Cube base image digest, grpc 1.85.0, OpenTelemetry 1.47.0, `golang.org/x/net`
0.62.0, protobuf 1.36.12, dspy 3.4.1, certifi 2026.10.23, pydantic-settings
2.16.2, kubectl 1.37.2, Helm 4.3.1, govulncheck 1.9.0 and gitleaks 8.30.1 remain
at their current releases. Four Python packages remain constraint-blocked
(`litellm` ≥1.104 against `filelock>=4.2`, `openai` 3.x and
`importlib-metadata` 10.x via litellm, `python-dateutil` 3.x/4.x via botocore).
`diskcache 5.6.3` retains an unfixed upstream advisory with no upgrade path; the
documented trusted-cache mitigation still applies.

## Substantive agent change: Pi 1.5.0

Upstream Pi Durable 1.5 removed `watchEvents()` and stopped resending a model
request that was in flight when the host died (the attempt is charged to
`settings.retry` instead). The ESF engine now derives its audit stream from the
conversation view exposed by `root.watch()` — entries, live run/tool/compaction
state and usage — and enables exactly one durable retry
(`enabled: true, maxRetries: 1, baseDelayMs: 100`) so the supervisor-restart
recovery contract and its regression test still hold. Non-retryable provider
errors remain single-shot. The bundle, notices, model catalog, runner version
string and inventory digests were regenerated, and all 17 Pi runner regressions
pass. The audit JSONL vocabulary and field set now follow the 1.5 conversation
view rather than the removed event stream; run/tool boundary events can be
missed when the watch folds two commits, and the bounded retry also applies to
retryable provider errors. Both are documented in
[docs/harness-pi.md](../../docs/harness-pi.md) and are evidence-fidelity notes,
not gate behavior; no factory gate parses the stream.

## Cube validation

Project execution used disposable local Cube microVMs. The new template is
`tpl-0914c5dd04cd48a0a5aa6be6` (alias `esf-v072-native-20261103`), image
`localhost:32001/esf-native@sha256:83c7f9fa…`, built from the tracked recipe plus
the operator native-CLI overlay and the Pi/Node overlay; every pinned payload
digest matched its pin. The primary in-Cube sandbox was
`a582a4a145f442058af193be76215688`; the acceptance runs used
`d654c05577b347fc86e1f4b10db67aa1` (conformance),
`a08ad75d88fa4f6c968c4c90a59ff4bd` (model-backed OpenCode) and
`8209b59a85674bf3972171572f74078e` (model-backed Pi).

- In-Cube Go tests: 17 compiled test binaries, **731 tests passed**, every run
  exit 0, on the new template.
- In-Cube release checks: `scripts/verify-release-inventory.py` and the three
  `scripts/tests` qualification-boundary tests passed.
- In-Cube secret scans: gitleaks 8.30.1 found no leaks in the working-tree
  pack and again in the final 655-file release tree, with sandbox cleanup
  verified each time. A host-side `git ls-files` scan of the release tree was
  likewise clean.
- Static checks: `make lint` (gofmt, `go vet`, shellcheck) and `govulncheck`
  v1.9.0 (no reachable vulnerabilities) passed.
- Console: 17/17 frontend tests, a deterministic production build (two builds
  produced 46/46 byte-identical files) and the rebuilt tracked `dist/`; npm
  audits for the console, OpenCode and Pi graphs reported zero vulnerabilities.
- Python: `uv.lock` refreshed; the local intake runtime was rebuilt on CPython
  3.14.10 with uv 0.13.7 (boto3/botocore 1.43.123, filelock 4.2.7, dspy 3.4.1),
  and `uv pip check` reports all 72 installed packages compatible. The CycloneDX
  1.6 SBOM was regenerated (329 components, valid serial number).
- `factory agents verify` matched all six configured agents and the Pi runtime
  self-check against the new template; `factory doctor` passed with the three
  known hardening warnings; `make cube-smoke` passed on the new template
  (node v26.13.0 in the VM) and verified zero leaks.
- Acceptance: `PHASE 1 ACCEPTANCE TEST: PASSED` three times with the 0.7.2 build on
  the upgraded stack — a conformance run (`run-c5c6d6d271b052482d540f59`), a
  model-backed OpenCode 2.2.5 run (`run-578cc37728348f206d7cdb40`) and a
  model-backed Pi 1.5.0 run (`run-7f1309adbe645092399e09a3`), all on
  `opencode-go/deepseek-v4.1-flash` where a model was used. Each produced a
  verified patch, passed the unchanged deterministic gates, and verified sandbox
  cleanup with zero remaining sandboxes; the Pi run exercises the 1.5.0 engine
  adaptation end to end. The model-backed runs' egress probe is
  `Unknown/ProbeInconclusive` because the local profile acknowledges open
  egress; it is not a denial-path failure.
- Local Temporal dev stack was upgraded and verified: server 1.32.2, UI 2.58.0,
  admin tools 1.32.1 on the retained PostgreSQL 18.6 volume, schemas 1.19/1.14
  unchanged and compatible, cluster SERVING and UI HTTP 200. A pre-upgrade
  database dump remains local (owner-only).
- CubeSandbox itself is already the latest published stable release (0.7.3);
  its one-click install, CLI, service units and proxy/lifecycle/egress images
  were verified and no server change was attempted (the session has no sudo).

## Local system

The historical local registry publish on `127.0.0.1:32000` is broken on this
host (a freshly created container on port 32000 also refuses connections), so
the 0.7.2 image chain was published and pulled through `127.0.0.1:32001`. This
is a local environment workaround, not a product requirement. Node 26.13.0 and
uv 0.13.7 were installed with mise; the local intake runtime moved to
`.factory/runtimes/intake-20261103-3.14.10`; `factory.toml` was repointed to the
new template, harness digests and runtime. The first acceptance dispatch was
rejected because a pre-existing worker from another session on the shared
`factory` task queue had an older startup configuration; the suite was re-run on
an isolated task queue and data directory and passed. No sandbox was leaked by
any run. Host Ubuntu package updates require administrator authentication, which
this session does not have, so no OS package upgrade was attempted.

## Independent review

Two independent, read-only reviews of the staged candidate were collected
before handoff. Both returned **approve with fixes** with no blocker or
high-severity findings: pins, digests, verifier scripts, console `dist`,
checksums, reproducibility and repository hygiene were confirmed consistent.
The supported findings were fixed before commit: the embedded inventory
baseline now records the candidate baseline, the Pi audit vocabulary and
boundary/retry limitations are documented, optional-harness archive digests
are pinned in both inventories, the evidence timestamps/sandbox IDs/scan
counts are consistent, and stale doc and helper-script wording is corrected.
One review noted that the candidate moved while it ran; the tree was frozen
and committed after the fixes.

## Remaining qualification

Image/template publication through the release workflows, model-backed
acceptance for the refreshed Claude/Codex/Unreal clients, hosted-provider
checks, the 24-hour VM and Kubernetes soak, the performance matrix, per-image
SBOM/vulnerability qualification, transitive-license review, restore, Temporal
history replay, migration drill, production deployment and eligible human
approval remain separate gates. Local unit and acceptance results do not satisfy
them, and this record is not production approval.
