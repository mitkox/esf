# Component upgrade validation, 2026-11-26

The candidate starts from `origin/master` `e5567fbf` (merge of the v1.0.0-rc.1
release branch) and refreshes every maintained component and dependency pin to
its current stable release, upgrades the matching locally installed components
and the local Temporal dev stack, rebuilds the local Cube template chain on the
new toolchain, and validates the result end to end. No factory behaviour,
configuration schema, workflow contract or result schema changes.

The distribution label is **v1.0.0-rc.2**; the binary, chart and Python
distribution version stays **1.0.0**. Upstream metadata is retained in
[the audit](evidence/component-upgrade-2026-11-26/upstream.json); build, runtime
and security evidence is retained beside this record.

## Pins

| Component | Previous | Candidate |
|---|---|---|
| ESF release | v1.0.0-rc.1 | **v1.0.0-rc.2** |
| Go toolchain and image | 1.27.3 | **1.27.4** |
| Node.js and image | 26.13.0 | **26.13.1** |
| uv | 0.13.7 | **0.13.10** |
| OpenCode (`@opencode/cli`) | 2.2.5 | **2.4.2** |
| Claude Code (native CLI) | 2.1.318 | **2.1.332** |
| Codex CLI (native) | 0.166.0 | **0.172.0** |
| Pi Durable / Pi AI / Chord | 1.5.0 | **1.6.1** |
| Temporal server / admin tools | 1.32.2 | 1.32.2 |
| Temporal UI | 2.58.0 | **2.58.1** |
| Temporal Go API | 1.64.1 | **1.64.3** |
| Temporal Go SDK | 1.50.0 | **1.52.0** |
| `golang.org/x/net` | 0.62.0 | **0.63.0** |
| PostgreSQL image | 18.6-alpine | **18.7-alpine** |
| Python boto3 / botocore | 1.43.123 | **1.44.12** |
| Python litellm / openai | 1.103.10 / 2.54.0 | **1.111.0 / 3.60.0** |
| Python filelock | 4.2.9 | **4.3.1** |
| Python certifi / pydantic-settings | 2026.10.23 / 2.16.2 | **2026.11.20 / 2.16.3** |
| Console lucide-react | 1.67.0 | **1.81.0** |
| Console tailwindcss / @tailwindcss/vite | 4.3.4 | **4.3.5** |
| Console @vitejs/plugin-react / vite | 6.2.1 / 8.3.5 | **6.3.0 / 8.4.0** |
| govulncheck | 1.9.0 | **1.10.0** |
| Helm (qualification pin) | 4.3.1 | **4.3.2** |
| kubectl (qualification pin) | 1.37.2 | **1.37.3** |
| Base image digests (golang, node, python, distroless, alpine) | rolling tags | refreshed digests |
| Service image digests (Temporal UI, PostgreSQL) | rolling tags | refreshed digests |

Unreal 0.3.1, CubeSandbox 0.7.3 (server and Go SDK), Temporal server and admin
tools 1.32.2, grpc 1.85.0, OpenTelemetry 1.47.0, protobuf 1.36.12, Cube base
image 2026.16, React/React DOM 19.3.1, esbuild 0.28.2, clsx 2.1.1 and gitleaks
8.30.1 remain at their current releases.

## Held pins

- **CubeSandbox stays 0.7.3 (server and Go SDK).** 0.8.0 is published, but the
  installed host stack (`/usr/local/services/cubetoolbox`, `release_version
  v0.7.3-amd64`) cannot be upgraded in this session: `sudo` requires a
  password, and the CubeMaster/Cubelet services are root-owned host units.
  `internal/sandbox/cube/create_compat.go` gates create-option compatibility on
  0.7.2/0.7.3 only, so adopting 0.8.0 requires a paired server upgrade, a new
  template, and a lifecycle requalification. The Go SDK was compiled against
  0.8.0 as a probe and reverted.
- **dspy stays 3.4.1.** dspy 3.5.0 removed `dspy.experimental.TypeSafe`, which
  `tools/intake/esf_intake/decision.py` imports for typed decisions. The 3.5.0
  lock resolves (and drops the `typesafe` extra) but the intake program stops
  importing, so the pin is held and the migration is queued rather than forced.
  With 3.4.1 the optional Python suites pass 8/8 and 7/7 and `uv pip check`
  reports all packages compatible; `diskcache 5.6.3` keeps its documented
  unfixed-advisory mitigation.

## Substantive changes

- **Cube template payload security.** The recipe rebuilds `envd` from the pinned
  e2b-dev/infra 2026.16 source with Go 1.27.4 and pins `golang.org/x/crypto
  v0.55.0` (plus x/mod v0.38.0, x/net v0.57.0, x/sync v0.22.0, x/text v0.41.0,
  x/tools v0.48.0) because the upstream module graph still requires x/crypto
  v0.49.0, which carries ten HIGH SSH advisories in the image scan. The rebuilt
  `envd` digest changed accordingly (`a90f5dc7…`, reproducible across two
  `--no-cache` rebuilds) and `go tool nm` confirms no `x/crypto/ssh` symbols are
  linked even before the bump. The template also pins
  `apt=2.4.15ubuntu0.1`/`libapt-pkg6.0=2.4.15ubuntu0.1` for CVE-2026-109518 and
  records both in `/opt/esf/apt-package-versions.txt`.
- **Pi 1.6.1.** The runner bundle, notices, embedded catalog (32 providers, 906
  models), inventory digests and template label were regenerated. The 1.6.x
  breaking changes are coding-agent extension APIs; the `pi-durable` watch,
  submit and usage contract is unchanged, so the 1.5.0 engine adapter keeps
  working. All 17 runner regressions pass with `--test-concurrency=1`. The suite
  is timing-sensitive on this host in parallel mode: the same flakiness
  reproduces on the previously qualified 1.5.0 bundle (17/17, 12/17, 15/17 across
  three parallel runs), so it is environmental and not attributable to 1.6.1.
- **OpenCode 2.4.2.** Package integrity, the linux/amd64 binary digest and the
  template check were regenerated. Model-backed acceptance was not possible in
  this environment, so the evidence is digest plus CLI startup inside the
  template, not a provider run.
- **CI uv checksum.** The review below caught that the `setup-uv` `checksum`
  input still held the 0.13.7 asset digest; it now holds the published 0.13.10
  `uv-x86_64-unknown-linux-gnu.tar.gz` digest (`bf65ac1a…`), verified against the
  release's `.sha256` and `sha256.sum`.

## Cube validation

Project execution used disposable local Cube microVMs; nothing but build and
driver code ran on the host.

- Template chain: `deploy/cube/Dockerfile` (envd, Unreal and OpenCode payloads)
  → `deploy/cube/Dockerfile.pi-v1` (Pi 1.6.1 bundle and Node 26.13.1) → the
  operator native-CLI overlay (digest-verified Claude 2.1.332 and Codex 0.172.0,
  local-only, not tracked). The Pi-enabled image is
  `127.0.0.1:32001/esf-native-pi@sha256:d2f4e458…` and the overlayed image is
  `…@sha256:d873387e…`. READY template:
  **`tpl-1f8854d780e7427da2d505c6`** (alias `esf-rc2b-native-20261126`, job
  `90b7896d-…`, artifact `rfs-1a7b3ea1…`, DNS 1.1.1.1, ports 49999/49983,
  `--enable-inject-envd=false`). Intermediate templates
  `tpl-79bb339a34804cc5a936ae0d` and `tpl-3a9f7a5a161942cdb408d29b` were
  superseded by the security patches and are recorded in the evidence.
- In-Cube suite (`bin/cube-test`) in sandbox `a48a2195…`: **15 compiled Go test
  binaries, every run exit 0** (692 passing test records reported by the runner),
  `scripts/verify-release-inventory.py` and the qualification-boundary tests
  passed in the same sandbox, gitleaks 8.30.1 scanned the 8.39 MB tracked source
  pack with no findings, and cleanup was verified with zero remaining owned
  sandboxes.
- `factory doctor` passed; `factory agents verify` matched all six configured
  agents on the new template (claude, codex, harness-opt-fix, opencode2, pi plus
  the Pi runtime self-check, unreal); `make cube-smoke` reported node v26.13.1
  in the VM and zero leaks.
- `make lint` (gofmt, go vet, shellcheck) passed; the Temporal schema-safety
  suite passed 6/6 scenarios; the CycloneDX 1.6 SBOM was regenerated (328
  components, valid serial number).
- Console unit tests 17/17 and a production build; the tracked `dist/` was
  regenerated.
- Acceptance: **`PHASE 1 ACCEPTANCE TEST: PASSED`** twice with
  `FACTORY_AGENT=conformance` on the new template —
  `run-47e74746b23a150a2e99ccb6` before and `run-570f5265eca3742617d1313e`
  after the Temporal dev stack upgrade. Both produced a verified patch, kept the
  gate tree-hash unchanged, scanned behaviour clean, and verified cleanup with
  zero remaining sandboxes. Model-backed runs were not possible: `/opt/esf/agents`
  is absent and the model endpoint `192.168.0.14:8000` is unreachable.

## Security verification

| Control | Result |
|---|---|
| `govulncheck 1.10.0 ./...` | No vulnerabilities found |
| `npm audit --audit-level=low` (console, OpenCode, Pi) | 0, 0, 0 |
| gitleaks 8.30.1 over the tracked source pack in Cube | no findings |
| trivy HIGH+CRITICAL on the final template image | **0** (2 OS and 10 Go advisory groups fixed in this candidate) |
| pip-audit over the exported lock | 1 unfixed advisory: `diskcache 5.6.3` (PYSEC-2026-2447), documented mitigation |
| Cube sandbox isolation | every run destroyed its sandbox; 0 remaining |

AI-assisted source security coverage is **not** included: the OpenCode/OpenCyber
model endpoint for the continuous-security workflow was unreachable. The
scanner evidence above is deterministic tooling and does not substitute for that
review. The `trivy` result applies to the amd64 template image; the factory
binary is covered by govulncheck, and the intake/runtime images are release
workflow artifacts that were not rebuilt here.

## Local system

- Toolchains installed with mise: Go 1.27.4, Node 26.13.1, uv 0.13.10, Python
  3.15.2, kubectl 1.37.3. Docker 29.1.3, Helm 4.3.2, gitleaks 8.30.1.
- The local Temporal dev stack moved to server 1.32.2, admin tools 1.32.2, UI
  2.58.1 and PostgreSQL 18.7-alpine on the retained `…-data-v18` volume. Schema
  versions stayed 1.19/1.14 (min compatible 1.0/0.1), the cluster reports
  SERVING, the `default` namespace exists and the UI answers HTTP 200. A
  pre-upgrade `pg_dumpall` is retained owner-only at
  `.factory/backups/temporal-pre-rc2-20261126.sql`.
- Docker Hub rate-limited anonymous pulls during this session; base and service
  images were pulled through `mirror.gcr.io` after confirming digest equality
  with the `docker.io` references, and the images were published through the
  working local registry on `127.0.0.1:32001` (32000 still refuses connections).
  This is a local environment workaround, not a product requirement.
- `factory.toml` and `.env` were repointed to the new template and agent
  digests. They stay gitignored and local.

## Independent review

An independent review was performed by `open-code-review 1.14.3` against
`opencode-go/deepseek-v4.1-flash` over the candidate change set (28 reviewed
files, 9 findings, no blocker). Seven findings were fixed before handoff:

1. `setup-uv` checksum pinned to the 0.13.7 asset while the version moved to
   0.13.10 (would have failed the Python job) — replaced with the published
   0.13.10 digest.
2. `cube-test-summary.json` used `go_test_binaries`/`release_checks` — restored
   the established `binary_count`/`check_count` names.
3. The summary flattened run kinds into `name` — restored discriminated
   `binaries` and `checks` lists.
4. `template-build.json` used `state` — restored `status`.
5. `template-build.json` used `payloads` — restored `payload_sha256`.
6. `acceptance.json` replaced the string `leak_check` with a count — restored the
   established `leak_check` string.
7. `security.json` reused `findings` for both an integer and an array — the
   Python advisory list is now `finding_details`.

Two findings were accepted without change:

- *Record the `envd` x/crypto patch set as an image label.* The patch set is
  already recorded in the recipe comment, the qualification record, the
  `upstream.json` audit and `template-build.json:repo_rebuilds`, and the payload
  digest is pinned. Adding a label would require another template rebuild and a
  full re-qualification for no new enforcement; it is queued as a follow-up.
- *Verify the copied payloads against `ARG` values instead of literals.* The
  literal checks are deliberate: a build-time `--build-arg` override must not be
  able to relax the digest gate. The recipe keeps literals; drift is caught by
  `scripts/verify-release-inventory.py` and the payload digest pin.

## Publication

- Candidate commit: `4c725c68` on `release/v1.0.0-rc.2` (130 files).
- Pull request: [#75](https://github.com/mitkox/esf/pull/75), CI run
  `45893242135` — Go, frontend, Pi runner, Python, macOS and the aggregate check
  all passed.
- Merged to `master` as `35f8dc8a`.
- Not performed: the `v1.0.0-rc.2` tag and the tag-triggered Release workflow.
  That workflow publishes archives, images and attestations, which is a separate
  authorisation; `git tag v1.0.0-rc.2 \&\& git push origin v1.0.0-rc.2` starts it when
  an operator decides to publish.

## Remaining qualification

Model-backed acceptance for the refreshed Claude, Codex, OpenCode and Pi clients;
hosted-provider checks; the 24-hour VM and Kubernetes soak; the performance
matrix; per-image publication through the release workflows; the CubeSandbox
0.8.0 server/SDK upgrade with template and lifecycle requalification; the dspy
3.5.0 intake migration; and AI-assisted source security coverage. Production
qualification gates from 1.0.0-rc.1 remain open.
