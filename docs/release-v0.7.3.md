# ESF 0.7.3 component and dependency refresh

0.7.3 keeps the 0.7.2 factory and console surface and refreshes the maintained
component and dependency pins to their current stable releases. It does not
change the `factory.toml` schema, the workflow contract, the durable
run/artifact layout or the result schema. Historical release records retain
their original pins.

## Changes

- Agents: Claude Code 2.1.317 -> **2.1.318** (official archive, extracted binary
  and `SHASUMS256.txt` digests pinned in the release inventory). OpenCode 2.2.5,
  Codex 0.166.0, Unreal 0.3.1 and Pi Durable/Pi AI/Chord 1.5.0 remain current.
- Temporal: admin tools 1.32.1 -> **1.32.2**, the current upstream image
  (`sha256:4a95866e...a754`). Server 1.32.2, UI 2.58.0, Go SDK 1.50.0 and Go API
  1.64.1 remain current. The dev-stack `.env.example` follows the same pin.
- Go modules: `github.com/grpc-ecosystem/grpc-gateway/v2` v2.32.0 -> **v2.33.0**
  and `go.yaml.in/yaml/v3` v3.0.5 -> **v3.0.6** (both indirect). All other 41
  modules, including grpc 1.85.0, OpenTelemetry 1.47.0, protobuf 1.36.12,
  `golang.org/x/net` 0.62.0 and the CubeSandbox Go SDK v0.7.3, are current.
- Python: `filelock` 4.2.7 -> **4.2.9** and `typing-inspection` 0.4.4 ->
  **0.4.5** (routine patches released after the 0.7.2 cut; neither is
  security-driven). The four constraint-blocked packages (`litellm`, `openai`,
  `importlib-metadata`, `python-dateutil`) remain unchanged because their
  upstream caps still conflict with the security-driven `filelock>=4.2.1`
  floor or the current `botocore`/`litellm` releases. `diskcache 5.6.3` keeps
  its unfixed upstream advisory; the documented disabled-disk-cache mitigation
  still applies and the package is not reachable in ESF flows.
- Console frontend: `lucide-react` 1.67.0 -> **1.68.0**. The tracked embedded
  `dist/` is rebuilt from source. React, Vite, Tailwind and the React plugin are
  unchanged and current.
- Cube template recipe: the template stage now installs the pinned Ubuntu 22.04
  security updates `openssl=3.0.2-0ubuntu1.30`, `libssl3=3.0.2-0ubuntu1.30` and
  `libbrotli1=1.0.9-2ubuntu0.1`, and records them in
  `/opt/esf/apt-package-versions.txt`. This removes the two High image findings
  (`CVE-2026-84782`, `CVE-2026-111497`) inherited from the base image. Refresh
  these pins with a new recipe if Ubuntu supersedes them. The other four template
  packages (`ca-certificates`, `git`, `python3`, `libatomic1`) remain unpinned,
  so a rebuild can drift their versions; the built image records the actual
  versions in `/opt/esf/apt-package-versions.txt`. The recipe hash is updated in
  the release inventory.
- CI: `astral-sh/setup-uv` v10.3.2 -> **v10.3.3** (still SHA-pinned).
- Documentation pin corrections: the private turn-executor deployment guide and
  example profile now pin Claude Code 2.1.318 with its binary SHA-256
  (`deploy/turn-executor/README.md`, `profiles.example.json`,
  `docs/sf-turn-executor.md`), and the OpenCode binary digest in
  `docs/opencode-go.md` is corrected to the shipped 2.2.5 payload hash.
- Toolchains and base images: Go 1.27.3, Node.js 26.13.0, Python 3.15.2
  (optional tooling) / 3.14.10 (`.python-version`), uv 0.13.7, kubectl 1.37.2,
  Helm 4.3.1, govulncheck 1.9.0 and gitleaks 8.30.1 are current. Container base
  digests are unchanged because upstream has not republished the pinned tags.

## Security validation

The refresh was validated with the pinned scanners:

- `govulncheck` v1.9.0 over the full module graph and the built binary, OSV and
  deps.dev cross-checks: no advisory at any severity on the shipped Go surface.
- `npm audit --audit-level=high` and OSV for the three shipped npm graphs
  (console, OpenCode, Pi): no findings.
- `pip-audit` with the exact lock pins and OSV for both interpreter closures:
  only the known unfixed `diskcache` Medium, which is not reachable.
- `grype` on the pinned images: the `temporalio/admin-tools` bump removes the
  2 Critical and 15 High fixed CVEs present in 1.32.1, and the pinned Ubuntu
  updates remove the two High findings from the template base. Remaining
  image-layer findings are upstream rebuilds that no current release fixes:
  the Cube base `envd` binary (Go 1.25.4; 2 Critical and 22 High), the Unreal
  agent runner (Go 1.27.0; 8 High), the Temporal UI (1 High in `x/net`), and the
  dev-only PostgreSQL 18.6-alpine image (8 Critical and 17 High in libcurl and
  brotli; upstream has not republished the digest, and adding an `apk upgrade`
  derived image was not applied, so this remains a qualification gap for the
  local Temporal stack). They are recorded as gaps, not waived.

## Upgrade notes

1. Build or install the 0.7.3 archive/image for the exact commit to be run; an
   existing `factory.toml` remains schema-compatible and evidence is read as-is.
2. The Cube template identity changes when the native CLI overlay is rebuilt for
   Claude Code 2.1.318: update `[cube] template_id` and the `claude`
   `binary_sha256` in each preinstalled harness profile, then restart the worker
   so it reloads the configuration.
3. The dev Temporal stack runs admin tools 1.32.2 with server 1.32.2 and UI
   2.58.0 on the retained PostgreSQL 18.6 volume.
4. Tagged release assets are published from immutable RC tags after the
   qualification gates run; a final tag is promoted from attested RC assets.

## Validation record

The local build, Cube-executed test suite (17 test binaries, 731 tests,
both release checks, clean secret scan, verified cleanup), template rebuild,
conformance acceptance run, `factory doctor` / `agents verify` / `cube-smoke`
and the post-refresh security re-scan are recorded in
[the 2026-11-04 qualification record](../release/qualification/component-upgrade-2026-11-04.md)
with evidence under `release/qualification/evidence/component-upgrade-2026-11-04/`.
A model-backed OpenCode acceptance run was attempted but did not complete
within the validation window (the external model provider did not return; the
run was cancelled and its sandbox cleaned up), so it is not cited as evidence.
Production qualification, image/template publication, soak, restore, replay
and hosted-provider checks remain separate gates.
