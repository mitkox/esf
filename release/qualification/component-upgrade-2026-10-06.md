# ESF 0.6.1 component upgrade — 2026-10-06

Dependency upgrades and factory functional validation passed on Linux/amd64
through the development CubeSandbox and Temporal stack. Route-aware Cubelet
egress is activated. All three network scenarios passed with the narrowly
scoped temporary callback firewall rule; persistent firewall approval is pending.
This evidence does not promote the release to production qualification.
OpenCode remains the default and unrelated checkout edits were preserved.

## Components

| Component | Previous | Selected |
| --- | --- | --- |
| Unreal agent | 0.2.0 | 0.3.1 |
| OpenCode V2 (`@opencode/cli`) | 2.0.18 | 2.0.24 |
| Pi Durable / Pi AI / Chord | 1.0.3 | 1.0.4 |
| Host Pi development CLI | 1.0.2 | 1.0.3 (mise release-age eligible) |
| Pi bundle esbuild | 0.25.12 | 0.28.2 |
| CubeSandbox server | 0.7.2 | 0.7.2 (current) |
| CubeSandbox Go SDK | 20260924-f1aaa737fb38 | 20260930-e02976ae5472 |
| Temporal server / admin tools | 1.32.0 | 1.32.0 (current) |
| Temporal UI | 2.54.1 | 2.55.0 |
| Temporal Go SDK / API | 1.49.0 / 1.63.6 | unchanged (current) |
| Machinist upstream revision | 39435164faf1 | unchanged (current upstream main) |
| OpenTelemetry | 1.46.0 | 1.47.0 |
| SQLite | 1.59.0 | 1.60.1 |
| Python | 3.14.7 | 3.14.8 |
| uv image installer | 0.11.14 | 0.12.23 |
| Vite / Lucide / jsdom | 8.3.1 / 1.48.0 / 30.1.1 | 8.3.3 / 1.52.0 / 30.1.2 |
| Gitleaks | 8.24.2 | 8.30.1 |

Go 1.27.1, Node 24.21.0, DSPy 3.4.0, React 19.3.0, and Tailwind 4.3.3
remain current for the selected toolchain tracks. Go, frontend, and optional
Python dependency lockfiles were refreshed. The source-map-js security fix
1.2.2 is included. GitHub Actions were updated to verified release commits for
Docker login/build/QEMU, Anchore SBOM, GitHub Script, and Codex Action.

The host Pi CLI's 1.0.4 release is excluded by the configured mise 24-hour
release-age rule until 2026-10-07 01:03 EEST. That protection was retained. The
separately pinned ESF Pi Durable 1.0.4 execution chain was built and tested
inside Cube.

Unreal 0.3.1 changed the release archive name to
`unreal-agent_0.3.1_linux_amd64.tar.gz`. The installer and template recipe now
use that name and verify the official archive, checksum file, and runner
digests. The installer downloads public assets with curl without requiring
GitHub credentials.

## Reproducible local artifacts

- Template: `tpl-e0bbde544dd84132bf656f76`.
- Image: `localhost:32000/esf-component-upgrade-pi:20261006`.
- Image index digest: `sha256:6d54d066fb73735bec5e95dccb2eaf3deec6bcc5156ec39a66a8ef5531882828`.
- Template resources: 4000 CPU millicores, 4096 MiB memory, 12 GiB writable layer.
- Development DNS: `1.1.1.1`, `8.8.8.8`; the old link-local resolver was
  unreachable from test VMs. Registry HTTPS connectivity passed with the new
  template. The callback firewall permission is separately recorded below.
- Pi runner digest: `018aec9e0215eff09aede13a2b707f6e6872d1dcf2b44727d5afc3d6e97c42fc`.

Published release and npm metadata were checked against their official
repositories/registries. Digest pins remain in the release inventory, the
embedded inventory, installer checks, and Cube template recipes.

## Verification

Project execution and tests ran inside Cube microVMs. The host built binaries
and drove the local factory and infrastructure.

- Full Go vet, race tests, and package builds passed, including Machinist,
  Temporal workflow/replay, control plane, factory, and Cube adapter suites.
- Pi: 17 runner regressions passed, covering durable recovery, bounded
  deadlines, refusal of unsafe shell replay, usage retention, and version pins.
  Initial timing failures during concurrent Go compilation did not reproduce
  in an idle VM; recovery behavior was not weakened.
- Frontend: 40 tests and the production Vite build passed. Embedded assets
  were rebuilt and obsolete hashed assets removed.
- Python 3.14.8: intake 8 tests, brief lab 7 tests, evals 71 tests, and the
  release qualification test passed. DSPy imports and pip dependency checks
  passed. The replacement local intake environment passed its CLI and both
  tool suites inside Cube before activation.
- Issue triage: 6 Node tests passed. Release/Pi inventory checks and CycloneDX
  SBOM structure checks passed.
- Fresh online OpenCode/Unreal installation passed all digest checks. Offline
  Pi installation passed; a modified Node binary was rejected before promotion.
- Gitleaks 8.30.1 reported no leaks. All three npm lockfile audits reported
  zero vulnerabilities. govulncheck 1.8.0 found zero vulnerable calls.
- Local Cube smoke and factory doctor passed. Temporal UI 2.55.0 served its
  settings and namespace API successfully against the existing server.

Final template acceptance used the same greeting-change fixture and local
`deepseek-v4-flash` endpoint. Each run produced a usable patch, passed
independent deterministic verification and gate-integrity checks, and
confirmed sandbox cleanup:

| Harness | Final run |
| --- | --- |
| Conformance | `run-51235ae365688465ebe0e374` |
| OpenCode 2.0.24 | `run-2fa4dcec57049433026a9329` |
| Unreal 0.3.1 | `run-8c1939ed63b13c55d22245cf` |
| Pi 1.0.4 | `run-b89de373c5ee06d210197ccf` |

The activated primary checkout also passed conformance
`run-0c2fbfb35d96b9c986af1d32` and Pi `run-65d0d1f44da2afeea407949c` with the
new Python intake environment enabled; both obtained live intake responses and
passed verification and cleanup. The integrated checkout, including the
pre-existing factory lock retry, passed focused race regressions inside Cube.

The activated 0.6.1 binaries subsequently passed release/Pi inventory checks,
the qualification-boundary regression, focused Go vet/race regressions, and
both binary version checks inside Cube. A final source secret scan found no
leaks. After activating route-aware routing, fresh factory acceptance passed:

| Harness | 0.6.1 run after routing |
| --- | --- |
| OpenCode | `run-eb463756890b8fb49951f945` |
| Unreal | `run-2327e4a977fd9b85b5c224fe` |
| Pi | `run-5524114d8e642753fcdd0dfd` |

Pi first failed safely when the model gateway ended its stream without
`finish_reason` (`run-b0ad997396bb450638007b4b`). No patch was accepted and
cleanup passed. A fresh run succeeded; both receipts are retained. This was
an external stream failure, not an automatic process-recovery qualification.
Independent Cube API listing and `factory sandboxes` confirmed zero live VMs.

For the local Pi profile, pass the non-secret placeholder expected by the
unauthenticated local gateway: `ESF_PI_LOCAL_MODEL_KEY=local-no-key
FACTORY_AGENT=pi bash scripts/factory-run-demo.sh`.

Local evidence is retained under `.factory/component-upgrade-20261006/`.
Configuration backups use the `.pre-component-upgrade-20261006` suffix.

## Cube network compatibility and callback routing

The deployed CubeAPI 0.7.2 create schema reads `allow_internet_access`, while
the published Go SDK sends `allowInternetAccess`. The factory adapter and
network probe now add the legacy denial field only for the operator-recorded
0.7.2 release; updates retain the correct camelCase shape. The regression
tests inspect actual SDK HTTP requests and passed with the race detector.
Live denial changed from public internet reachable to unreachable after the
repair. The probe now also fails if a deny-internet scenario reaches the
internet, rather than checking host reachability alone. It resolves the public
target on the driver and forces direct curl connections with TLS verification,
so sandbox DNS failure or proxy settings cannot substitute for this check.

OpenCode additionally passed `run-f818696832e2a8212ecfc5cc` with public internet
denied and only the local model allowlisted. The live intake response, agent
patch, deterministic verification, and cleanup passed. Factory egress
measurement remained `ProbeInconclusive`; this run is functional evidence and
does not replace a complete security/egress receipt.

The old and upgraded templates initially failed callbacks to the factory
host, including explicitly allowlisted requests. The operator authorized the
supported route-aware mode: Cubelet now has `cube_router_enable = true`.
The prior configuration was preserved and Cubelet was restarted with no live
sandboxes. The service and create/execute/destroy smoke passed after activation.

Packet tracing showed host-local callbacks enter on `cube-dev` with the VM's
allocated `172.31.64.0/18` address. A temporary firewall permission limited to
that interface and source range, destination `192.168.0.16`, TCP port `18080`
passed all three scenarios:

| Scenario | Host callback | Public direct HTTPS |
| --- | --- | --- |
| Default, no host allowlist | blocked | reachable |
| Explicit host allowlist | reachable | reachable |
| Internet denied, explicit host allowlist | reachable | blocked |

The unused provisional `cube-router` callback rule was removed. Automatic
approval review rejected persisting the verified `cube-dev` rule because it
changes the shared host firewall for future sandboxes and required exact user
authorization. That decision remains pending. The temporary rule was removed after the
successful probe; no callback permission was persisted. A temporary-rule pass
does not establish persistent callback readiness. The routing/configuration backup and
bounded probe logs are retained in the local evidence directory.

## Remaining qualification boundaries

The current stable gRPC 1.84.0 has advisory
[GO-2026-6443](https://pkg.go.dev/vuln/GO-2026-6443), a server panic involving
missing authority/Host headers. govulncheck found the package but no vulnerable
calls from ESF. The listed fix is a development prerelease; no unqualified
prerelease was substituted for the current stable dependency.

Pi remains opt-in. Hosted HTTPS CubeEgress acceptance, model-backed recovery,
failure and review cases, Kubernetes, production TLS, restore and soak testing
remain separate gates. Local runs use the existing permissive development
egress profile and a root template; they do not establish production egress or
non-root qualification. GitHub Actions validation is reported against the
exact source head on [PR #35](https://github.com/mitkox/esf/pull/35).
