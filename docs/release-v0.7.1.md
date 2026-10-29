# ESF 0.7.1 component and dependency refresh candidate

0.7.1 keeps the 0.7.0 factory and console surface and refreshes every
maintained component, toolchain and dependency pin to its current stable
release. It does not change the `factory.toml` schema, the workflow contract or
the durable evidence layout. Historical release records retain their original
pins.

## Changes

- Go modules: `google.golang.org/grpc` 1.84.0 → 1.85.0 and
  `golang.org/x/net` 0.61.0 → 0.62.0. The x/net bump fixes two `x/net/html`
  advisories that are not import-reachable from ESF (only `x/net/trace` is
  used); `govulncheck` reports no reachable vulnerability. Temporal SDK/API,
  OpenTelemetry 1.47.0, CubeSandbox SDK v0.7.3 and protobuf remain at current
  releases. Go 1.27.3 remains the pinned current security release.
- Agents: OpenCode 2.1.4 → **2.2.0**, Claude Code 2.1.300 (deployed) /
  2.1.311 (inventory) → **2.1.312**, Codex 0.162.1 (deployed) /
  0.164.0 (inventory) → **0.165.0**. Unreal stays 0.3.1 and Pi stays 1.4.0.
  Binary, tarball and npm-integrity digests are refreshed in the release
  inventory; the Cube template verifies them at build time.
- Python: CPython 3.14.7 → **3.14.10** for the optional tooling and the local
  intake runtime. `uv.lock` refreshes boto3/botocore to 1.43.120 and
  pydantic-settings to 2.16.2; dspy remains 3.4.1 (current) and the
  security-driven minimums remain certifi 2026.10.23 and filelock 4.2.4. Four
  packages (`litellm`, `openai` 3.x, `importlib-metadata` 10.x,
  `python-dateutil` 3.x) remain constraint-blocked; `litellm ≥1.104` conflicts
  with the root `filelock>=4.2.1` minimum. `diskcache 5.6.3` (transitive via
  dspy) has an unfixed upstream advisory and no upgrade path; the documented
  mitigation is a trusted cache directory.
- Toolchains: Kubernetes qualification 1.37.1 → **1.37.2**. Go 1.27.3, Node
  26.12.1, uv 0.13.6, Helm 4.3.1 and PostgreSQL 18.6 are current.
- Console frontend: `lucide-react` 1.63.0 and `vite` 8.3.5; the rest of the
  frozen npm graph is current and `npm audit` reports no vulnerabilities.
- Images: immutable digests refreshed for the rolling official tags with
  unchanged versions — node 26.12.1-slim, debian trixie-slim, golang 1.27.3 and
  python 3.15.2-slim. Cube base, distroless runtime, Temporal and PostgreSQL
  image digests remain current. The Cube template recipe is updated for the new
  OpenCode pin and base digests and rebuilt locally as
  `esf-v071-native-20261028`.

## Upgrade notes

1. Build or install the 0.7.1 archive/image for the exact commit to be run; an
   existing `factory.toml` remains schema-compatible and evidence is read as-is.
2. The optional local intake runtime path moves to
   `.factory/runtimes/intake-20261028-3.14.10`; update `[intake]
   python_executable` when using a local runtime.
3. The Cube template identity changed. Update `[cube] template_id` and each
   preinstalled harness `binary_sha256` (opencode2, claude, codex; the Pi node
   runtime digest is unchanged), then restart the worker so it reloads the
   configuration.
4. `v0.7.1-rc.N` tags build reproducible, attested release candidates.
   Promotion to a final `v0.7.1` release requires the attested companion
   qualification manifest and the deployment matrix described in the release
   procedure; the RC is a validation artifact, not production approval.

## Validation boundary

Local validation for this refresh is recorded in
[the component-upgrade qualification record](../release/qualification/component-upgrade-2026-10-28.md):
Go build/vet/format, `govulncheck` (no reachable vulnerabilities), release and
Pi inventory checks, the console build with 17 frontend tests and zero npm
audit findings, 689 Go tests executed in disposable Cube microVMs through
`tools/cube-test`, the release inventory and qualification checks inside Cube,
`factory agents verify` against the rebuilt template, a conformance acceptance
run with the committed build that passed the deterministic gates with verified
sandbox cleanup (its egress probe is inconclusive because the local profile
acknowledges open egress), and `factory doctor` / `make cube-smoke` on the new
template. The `git ls-files` release tree is secret-scan clean; the only
scanner hits in a full working-directory scan are gitignored local caches.
GitHub CI remains authoritative for the unit suites.

The following remain separate gates and are **not** claimed by this candidate:
the 24-hour VM and Kubernetes soak, the concurrency 1/4/8 performance matrix,
per-image SBOM/vulnerability qualification, transitive-license review, hosted
provider acceptance, production deployment, restore, replay and migration
drills. Passing local checks does not satisfy them, and this candidate is not
production approval.
