# ESF 1.0.0-rc.2 — component and dependency refresh

1.0.0-rc.2 is a component refresh of the [1.0.0 release candidate](release-v1.0.0-rc.1.md).
It moves every maintained pin to its current stable upstream release, regenerates
the lockfiles, agent bundles and embedded console, rebuilds the local Cube
template chain on the new toolchain, and re-runs the validation suite. No
factory behaviour, configuration schema, workflow contract or result schema
changes.

The binary, archive, chart and Python distribution version stays **1.0.0**; the
distribution tag is **v1.0.0-rc.2**. Runs produced by this build record
`factory_version = 1.0.0`. Production qualification is still incomplete: the
outstanding GA gates from 1.0.0-rc.1 remain open.

## Refreshed pins

| Component | 1.0.0-rc.1 | 1.0.0-rc.2 |
|---|---|---|
| Go toolchain / image | 1.27.3 | **1.27.4** |
| Node.js / image | 26.13.0 | **26.13.1** |
| uv | 0.13.7 | **0.13.10** |
| OpenCode (`@opencode/cli`) | 2.2.5 | **2.4.2** |
| Claude Code (native CLI) | 2.1.318 | **2.1.332** |
| Codex CLI (native) | 0.166.0 | **0.172.0** |
| Pi Durable / Pi AI / Chord | 1.5.0 | **1.6.1** |
| Temporal server / admin tools | 1.32.2 | 1.32.2 |
| Temporal UI | 2.58.0 | **2.58.1** |
| Temporal Go API / SDK | 1.64.1 / 1.50.0 | **1.64.3 / 1.52.0** |
| `golang.org/x/net` | 0.62.0 | **0.63.0** |
| PostgreSQL image | 18.6-alpine | **18.7-alpine** |
| Python boto3 / botocore | 1.43.123 | **1.44.12** |
| Python litellm / openai | 1.103.10 / 2.54.0 | **1.111.0 / 3.60.0** |
| Python filelock / certifi / pydantic-settings | 4.2.9 / 2026.10.23 / 2.16.2 | **4.3.1 / 2026.11.20 / 2.16.3** |
| Console lucide-react | 1.67.0 | **1.81.0** |
| Console tailwindcss / @tailwindcss/vite | 4.3.4 | **4.3.5** |
| Console @vitejs/plugin-react / vite | 6.2.1 / 8.3.5 | **6.3.0 / 8.4.0** |
| govulncheck | 1.9.0 | **1.10.0** |
| Helm (qualification pin) | 4.3.1 | **4.3.2** |
| kubectl (qualification pin) | 1.37.2 | **1.37.3** |

Base image digests were refreshed for golang, node, python, distroless, alpine,
PostgreSQL and Temporal UI. Unreal 0.3.1, CubeSandbox 0.7.3, grpc 1.85.0,
OpenTelemetry 1.47.0, protobuf 1.36.12, React 19.3.1, esbuild 0.28.2 and
gitleaks 8.30.1 are unchanged.

## Held pins

- **CubeSandbox stays 0.7.3 (server and Go SDK).** 0.8.0 is published, but the
  installed host stack is 0.7.3 and upgrading the CubeMaster/Cubelet services
  needs administrator rights that this environment does not have. The
  create-options compatibility shim is version-gated on 0.7.2/0.7.3, so a paired
  server-plus-SDK upgrade with template and lifecycle requalification is the
  follow-up.
- **dspy stays 3.4.1.** dspy 3.5.0 removed `dspy.experimental.TypeSafe`, which the
  optional intake decision program imports. Moving to 3.5.0 is an intake
  behaviour migration, not a pin refresh.

## Security

The candidate was scanned with deterministic tooling:

- `govulncheck 1.10.0`: no reachable vulnerabilities in the Go module graph.
- `npm audit` on the console, OpenCode and Pi graphs: zero vulnerabilities.
- gitleaks 8.30.1 over the tracked source pack inside a disposable sandbox: no
  findings.
- trivy on the final template image: **no HIGH or CRITICAL findings**. Two
  upstream groups were fixed in this release rather than accepted: the base
  image's `apt`/`libapt-pkg6.0` advisory (CVE-2026-109518) via pinned
  `2.4.15ubuntu0.1`, and ten `golang.org/x/crypto` SSH advisories reported
  against `/usr/bin/envd` by rebuilding that payload with `x/crypto v0.55.0`
  (see [the template recipe](../deploy/cube/README.md)).
- pip-audit reports one unfixed advisory: `diskcache 5.6.3` (PYSEC-2026-2447),
  which has no upstream fix and keeps its documented trusted-cache mitigation.

AI-assisted source security coverage was **not** run: the OpenCode/OpenCyber
model endpoint is unreachable from this environment. The tool evidence above
does not replace that coverage.

## Validation

- 15 compiled Go test binaries executed in disposable Cube sandboxes on
  `tpl-1f8854d780e7427da2d505c6`; every run exited 0 and the release inventory
  and qualification-boundary checks passed in the same sandbox.
- Console unit tests 17/17 and a production build; the Pi runner regression
  suite 17/17 with `--test-concurrency=1`; optional Python suites 8/8 and 7/7
  with `uv pip check` clean.
- `make lint`, the Temporal schema-safety scenarios (6/6) and the CycloneDX 1.6
  SBOM (328 components) were regenerated.
- `factory doctor` passed, `factory agents verify` matched all six configured
  agents and the Pi runner self-check, and `make cube-smoke` reported node
  v26.13.1 inside the VM.
- Acceptance: `PHASE 1 ACCEPTANCE TEST: PASSED` with `FACTORY_AGENT=conformance`
  on the new template, including a re-run after the local Temporal stack moved
  to UI 2.58.1 and PostgreSQL 18.7. Every run verified cleanup with zero
  remaining sandboxes.

Model-backed acceptance was not run in this environment (`/opt/esf/agents` is
absent and the model endpoint is down). The refreshed Claude, Codex, OpenCode
and Pi clients were verified by archive/binary digest and CLI startup inside the
template, which is not the same evidence as a model-backed acceptance run.

## Upgrade notes

- The template recipe now pins `apt`/`libapt-pkg6.0` and rebuilds `envd` with
  `golang.org/x/crypto v0.55.0`. Templates and image digests change only with
  requalification; do not mix a 1.0.0-rc.1 template with this build.
- Operators who track the optional Python tools need to re-sync `uv.lock`;
  dspy remains at 3.4.1.
- The full record, with upstream metadata and machine-readable evidence, is in
  [the qualification record](../release/qualification/component-upgrade-2026-11-26.md).
