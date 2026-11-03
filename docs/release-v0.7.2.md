# ESF 0.7.2 component and dependency refresh candidate

0.7.2 keeps the 0.7.1 factory and console surface and refreshes every maintained
component, toolchain and dependency pin to its current stable release. It does
not change the `factory.toml` schema, the workflow contract, the durable
run/artifact layout or the result schema. The Pi audit event vocabulary follows
the Pi 1.5 view API and is documented in
[docs/harness-pi.md](harness-pi.md). Historical release records retain their
original pins.

## Changes

- Go modules: `go.temporal.io/api` 1.64.0 → **1.64.1** to match Temporal server
  1.32.2. `google.golang.org/grpc` 1.85.0, OpenTelemetry 1.47.0,
  `golang.org/x/net` 0.62.0, protobuf 1.36.12, Temporal Go SDK 1.50.0 and the
  CubeSandbox Go SDK v0.7.3 remain at their current releases.
- Agents: OpenCode 2.2.0 → **2.2.5**, Claude Code 2.1.312 → **2.1.317**, Codex
  0.165.0 → **0.166.0**. Unreal stays 0.3.1. Binary, tarball and npm-integrity
  digests are refreshed in the release inventory. The tracked Cube template
  verifies the OpenCode, Unreal and Pi payloads at build time; Claude and Codex
  are operator-provisioned, and their archive and binary digests are pinned in
  the inventory and verified by the operator workflow.
- Pi: Pi Durable / Pi AI / Chord 1.4.0 → **1.5.0**, bundled with esbuild and
  rebuilt for Node 26.13.0. Upstream 1.5 removed `watchEvents()` and no longer
  resends an in-flight request after a host crash; the ESF engine now derives
  its audit stream from the conversation view and enables exactly the one
  durable retry the supervisor-restart contract requires; the same bounded setting
  re-sends a retryable provider error once (stream retries stay zero). The runner
  version string, model catalog, third-party notices and inventory digests are
  refreshed, and the audit vocabulary/limitations are documented in
  [docs/harness-pi.md](harness-pi.md).
- Toolchains: Node.js 26.12.1 → **26.13.0**; uv 0.13.6 → **0.13.7**. Go 1.27.3,
  Python 3.15.2 (optional tooling) / 3.14.10 (`.python-version` and the local
  intake runtime), Kubernetes qualification 1.37.2 and Helm 4.3.1 are current.
- Temporal: server 1.32.1 → **1.32.2**, UI 2.57.0 → **2.58.0**; admin tools stay
  1.32.1 because upstream does not publish a 1.32.2 admin-tools image. The Go
  API moves to 1.64.1. Local management uses the patched server, the UI and the
  same PostgreSQL 18.6 data volume.
- CubeSandbox: server and Go SDK remain **0.7.3**, the current stable release;
  the ghcr.io/tencentcloud/cubesandbox-base image digest is unchanged. The ESF
  Cube template recipe is refreshed for the new OpenCode and Node pins and
  rebuilt locally.
- Python: `uv.lock` refreshes boto3/botocore to **1.43.123**, filelock to
  **4.2.7** (above the `filelock>=4.2.1` security floor), minor updates for
  anyio and huggingface-hub, and patch updates for cachetools, hf-xet,
  json-repair, litellm (still <1.104), pydantic and pydantic-core. dspy remains 3.4.1, certifi
  2026.10.23 and pydantic-settings 2.16.2. Four packages remain
  constraint-blocked (`litellm` ≥1.104 against `filelock>=4.2`, `openai` 3.x
  and `importlib-metadata` 10.x via litellm, `python-dateutil` 3.x/4.x via
  botocore). `diskcache 5.6.3` retains an unfixed upstream advisory with no
  upgrade path; the documented trusted-cache mitigation still applies.
- Console frontend: `lucide-react` 1.63.0 → **1.67.0**, `tailwindcss` and
  `@tailwindcss/vite` 4.3.3 → **4.3.4**, `@vitejs/plugin-react` 6.1.2 →
  **6.2.1**. React 19.3.1 and Vite 8.3.5 are current. The tracked embedded
  `dist/` is rebuilt from source.
- Images: immutable digests refreshed for the rolling official tags —
  golang 1.27.3, node 26.13.0-slim, Python 3.15.2-slim, Debian trixie-slim,
  distroless static-debian13, Temporal server 1.32.2 and UI 2.58.0. PostgreSQL
  18.6-alpine and Cube base digests are unchanged. The Cube template recipe
  hash is refreshed.

## Upgrade notes

1. Build or install the 0.7.2 archive/image for the exact commit to be run; an
   existing `factory.toml` remains schema-compatible and evidence is read as-is.
2. For local development only, the optional intake runtime path moves to
   `.factory/runtimes/intake-20261103-3.14.10`; update `[intake]
   python_executable` when using a local runtime.
3. The Cube template identity changed. Update `[cube] template_id` and each
   preinstalled harness `binary_sha256` (opencode2, claude, codex, the Pi runner
   and the Pi Node runtime; the Unreal digest is unchanged), then restart the
   worker so it reloads the configuration.
4. The local development stack publishes images through the loopback registry.
   On this host the historical `127.0.0.1:32000` publish is broken (a fresh
   container on port 32000 also refuses connections); the 0.7.2 template chain
   was built and registered through `127.0.0.1:32001`. This is a local
   environment workaround, not a product requirement.
5. The local Temporal dev stack runs server 1.32.2 and UI 2.58.0 with admin
   tools 1.32.1 on the retained PostgreSQL 18.6 volume. A pre-upgrade database
   backup is retained locally.
6. `v0.7.2-rc.N` tags build reproducible, attested release candidates.
   Promotion to a final `v0.7.2` release requires the attested companion
   qualification manifest and the deployment matrix described in the release
   procedure; the RC is a validation artifact, not production approval.

## Validation boundary

Local validation for this refresh is recorded in
[the component-upgrade qualification record](../release/qualification/component-upgrade-2026-11-03.md):
build, `make lint` (gofmt, `go vet`, shellcheck), `govulncheck` (no reachable
vulnerabilities), release and Pi inventory checks, 17 compiled Go test binaries
with **731 tests passing inside disposable Cube microVMs**, the release
inventory and qualification checks plus a gitleaks scan inside Cube, the console
build with 17/17 tests, a deterministic `dist/` and zero npm-audit findings,
17/17 Pi runner regressions, the refreshed Python lock with a rebuilt CPython
3.14.10 intake runtime, and a CycloneDX 1.6 SBOM (329 components).
`factory agents verify` matched six agents and the Pi runtime self-check against
the rebuilt template, `factory doctor` passed, and `make cube-smoke` verified the
template and zero leaks. Three acceptance runs with the 0.7.2 build on the
upgraded local stack passed the deterministic gates, produced verified patches
and verified sandbox cleanup: a conformance run
(`run-c5c6d6d271b052482d540f59`), a model-backed OpenCode 2.2.5 run
(`run-578cc37728348f206d7cdb40`) and a model-backed Pi 1.5.0 run
(`run-7f1309adbe645092399e09a3`), the model-backed runs on
`opencode-go/deepseek-v4.1-flash`. Their egress probe is
`Unknown/ProbeInconclusive` because the local profile acknowledges open egress.
The `git ls-files` release tree is secret-scan clean. GitHub CI remains
authoritative for the unit suites.

The following remain separate gates and are **not** claimed by this candidate:
the 24-hour VM and Kubernetes soak, the concurrency 1/4/8 performance matrix,
per-image SBOM/vulnerability qualification, transitive-license review, hosted
provider acceptance, production deployment, restore, replay and migration
drills. Passing local checks does not satisfy them, and this candidate is not
production approval.
