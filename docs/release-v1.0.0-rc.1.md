# ESF 1.0.0-rc.1 — first release candidate of 1.0.0

1.0.0-rc.1 is the first release candidate of ESF 1.0.0: a production-oriented candidate of
the factory surface that has been exercised through the 0.7.x release line. It
removes the unqualified Harness2 improvement proof of concept, freezes the 1.0.0
release metadata, and carries a semver prerelease tag so it cannot be mistaken
for the final 1.0.0 release.

The binary, archive, chart and Python distribution version is **1.0.0**; the
distribution tag is **v1.0.0-rc.1**. Runs produced by this build record
`factory_version = 1.0.0`. The release-candidate label is deliberate: production qualification
is not complete, and the [outstanding GA gates](#outstanding-ga-gates) below must
close before 1.0.0 is announced as production-ready.

This release does not change the retained `factory.toml` schema, the Temporal
workflow contract, the durable run/artifact layout or the result schema. The only
removals are the Harness2 surfaces listed below. Historical release notes and
qualification records keep their original pins and statements.

## Changes

- **Version and release metadata:** 0.7.3 → **1.0.0** in the binary, release
  archives, container images, Helm chart and Python metadata, with
  `v1.0.0-rc.1` as the prerelease distribution tag. `release/inventory.json`
  records release `v1.0.0` and the branch baseline; its embedded copy in the
  binary is refreshed from the same pins. Release tooling accepts and verifies
  semver prerelease tags and keeps the final-promotion path unchanged.
- **Harness2 improvement proof of concept removed:** CLI commands, run flags,
  configuration sections, deploy overlay, vendored provenance, documentation and
  the Harness2-specific qualification record are gone. See
  [the migration section](#harness2-poc-removal-and-migration) and
  [ADR 0009](adr/0009-remove-harness2-poc.md).
- **Release packaging:** the Harness2 license/notice files, SBOM entries and
  third-party provenance are no longer copied into release archives or
  generated SBOMs. A factory archive now contains only `LICENSE`, `README.md`,
  `factory.example.toml` and the `factory` binary.
- **Qualification tooling:** the `tools/cube-test` sandbox id prefix changed from
  `harness2-validation-*` to `esf-validate-*`. This only affects the sandbox IDs
  visible in test-tool runs.
- **Documentation:** new 1.0.0-rc.1 release notes and ADR 0009, updated
  documentation index and README status, and removal of the Harness2 operator
  guide. Historical records are retained.
- **Security-driven pin refreshes:** the source-first review and CVE scans were
  re-run against the 1.0.0 surface (see
  [security validation](#security-validation)). Any base-image, package or
  dependency pin that changed as a result is recorded in
  [`release/inventory.json`](../release/inventory.json); this release does not
  otherwise change the 0.7.3 component line.
- **Retained behavior:** ordinary factory runs, deterministic verification,
  review gates, the read-only console, the private sf turn service, the optional
  Claude Code/Codex/Pi harness adapters, hardening and cleanup semantics are
  unchanged.

## Harness2 PoC removal and migration

ESF 0.7.x carried an experimental Harness2 Propose–Probe–Compose implementation:
immutable instructional profiles, offline experiment suites, a loopback
`budget-gateway` model route with a durable budget ledger, signed promotion
receipts, and adapted upstream role instructions pinned under
`third_party/harness2/`. It never reached production qualification: the inventory
recorded `production_qualified: false`, and no approved profile, model-backed
quality evidence, pricing attestation or deployment qualification existed.
Keeping a second, unqualified execution engine with its own credentials, budget
state and workflow types was an unacceptable 1.0.0 support and attack surface.
[ADR 0009](adr/0009-remove-harness2-poc.md) records the full rationale.

### Removed surface

| Area | Removed in 1.0.0-rc.1 |
| --- | --- |
| CLI commands | `factory improve`; `factory profile` including `import`, `inspect`, `qualify`, `promote` and `approval-payload`; `factory budget-gateway` including `budget-gateway digest` |
| CLI flags / manifest fields | `factory run --harness-profile`, `--improvement-policy`; `harness_profile` and `improvement_policy` in reviewed `factory apply` manifests; the `harness_profile`, `harness_profile_digest` and `improvement` fields in new run manifests |
| Temporal workflow types | `ImprovementProbeWorkflow`, `ImprovementSuiteWorkflow` and `ImprovementTrialWorkflow` are no longer registered; the change workflow no longer routes on an improvement policy |
| Configuration | `[harness_profiles]`, `[improvement]`, `[improvement_policies]`, `[improvement_suites]`; `scopes.<name>.harness_profiles` and `scopes.<name>.improvement_policies` |
| Source and provenance | `internal/harnessprofile/`, `internal/inferencebudget/` (used only by the removed commands), the improvement packages/wiring in `internal/factory/`, `cmd/factory/improvement.go`, `deploy/harness-improvement/`, `third_party/harness2/` |
| Documentation and records | `docs/harness-improvement.md`; the 2026-10-19 Harness2 integration qualification record and its evidence |
| Release qualification | the companion-manifest `harness_improvement` section and its HARNESS2 commit/provenance, quality and approval checks; Harness2 SBOM components |

No upstream Harness2 runtime was ever shipped: only ESF-native code and adapted
instruction text were used, so no upstream Python runner or installer dependency
is removed from operator deployments.

Run manifests written by 1.0.0-rc.1 omit the removed `harness_profile`,
`harness_profile_digest` and `improvement` evidence fields. Manifests written by
earlier releases are still read as-is, so `factory get`/`describe --json`
consumers must tolerate those fields being absent on new runs.

### Operator migration

1. **Remove the removed configuration before upgrading.** Configuration decoding
   rejects unknown fields, so `factory config validate` (and worker startup) fail
   loudly if any of the sections or scope keys in the table above remain in
   `factory.toml`. Delete them; there is no migration tool and no data to
   migrate because the factory never read them.
2. **Remove the removed CLI flags from automation.** Any script, reviewed
   `factory apply` manifest, CI job or runbook that passes `--harness-profile`,
   `--improvement-policy`, a `harness_profile` manifest field or an
   `improvement_policy` manifest field now fails with an unknown-flag/field
   error. Drop the option; choose the agent and `--verification` profile as
   before.
3. **Retire gateway deployments.** Stop any `factory budget-gateway` process,
   remove its reverse-proxy route and its loopback listener, and rotate/retire
   the gateway control and token secrets if they are not used elsewhere. Remove
   the model resource that targeted the gateway (for example
   `[models.improvement]`) and any scope reference to it: unlike the removed
   sections it is still valid generic configuration, so validation will not flag
   it, but the gateway endpoint no longer exists. The gateway budget ledger,
   profile store, promotion receipts and experiment archives are not read by
   1.0.0-rc.1; archive or delete them under your retention policy.
4. **Drain in-flight improvement work.** The improvement workflow types and
   their workers no longer exist, so those workflows cannot resume or replay on a
   1.0.0-rc.1 worker. Finish or cancel them before stopping the 0.7.3 worker.
5. **Remove the deployment overlay.** Delete `deploy/harness-improvement/` from
   deployment automation and image builds; the directory no longer exists in the
   release tree.
6. **Remove Harness2 from companion qualification manifests.** The
   `harness_improvement` section and its Harness2 commit, profile, route,
   quality, approval and activation checks are gone from the release
   qualification tooling. Delete the section from manifest templates; it is no
   longer required or evaluated.
7. **No replacement is provided.** There is no 1.0.0 improvement pipeline. Plain
   factory runs, verification and review gates remain the supported way to
   compare harness behavior using retained evidence.

## Security validation

**Requirement:** zero Critical and zero High findings on ESF product artifacts
(factory archive/binary and image, intake image, console SPA, OpenCode/Pi npm
graphs, Python tool closures, Cube template recipe output, Helm chart, scripts
and deploy manifests). Findings in upstream artifacts ESF does not build — for
example the Cube vendor `envd` base, the Temporal images, optional agent runners
and the dev-only PostgreSQL image — are either upgraded/patched or recorded as
explicit inherited platform risks with owner, reachability and a GA gate. They
are never silently waived. The source tree and release archives must also pass a
clean secret scan.

**Method:** source-first security review of the 1.0.0 shipped surface plus
pinned scanners: `govulncheck` 1.9.0 over the module graph and the built binary;
`npm audit` 11.20.0 and OSV for the console, OpenCode and Pi graphs; `pip-audit`
2.10.1 and OSV for the exact `uv.lock` interpreter closures; `grype` 0.121.1
with the current vulnerability database (DB 2026-11-05T06:21:06Z) for every
shipped and platform image, including rebuilt images and the Cube template base;
and `gitleaks` 8.30.1. Evidence is retained under
`release/qualification/evidence/v1.0.0-rc.1/security/`; scanner versions and
database dates are listed below.

> **Final results (evidence-backed).** All ESF product artifacts below meet the
> zero Critical/High requirement. Remaining findings are in upstream artifacts
> and are recorded as explicit inherited risks with an operator or GA gate; none
> is silently waived.

| Surface | Result |
| --- | --- |
| Factory binary + release archives (govulncheck 1.9.0 source+binary, OSV) | 0 Critical / 0 High |
| Factory container image (grype) | 0 / 0 (no matches) |
| Intake container image (grype; rebuilt on pinned Alpine 3.23.7 + uv-managed CPython 3.15.2) | 0 / 0 (5 Medium, 1 Low) |
| Console SPA npm graph (npm audit + OSV) | 0 / 0 |
| OpenCode and Pi npm graphs (npm audit + OSV) | 0 / 0 |
| Python optional tooling `uv.lock` (pip-audit + OSV, 3.14 and 3.15 closures) | 0 / 0; only `diskcache` 5.6.3 Medium unfixed, vulnerable path disabled |
| Cube template recipe output (grype; `envd` rebuilt from `e2b-dev/infra` tag 2026.16 and Unreal runner rebuilt from `unreallabsai/unreal-agent` v0.3.1 with Go 1.27.3) | 0 / 0 (was 2 Critical / 30 High) |
| Full native+Pi template image (grype) | 0 / 0 |
| Helm chart, scripts, deploy manifests (source review + gitleaks 8.30.1 over the committed tree (585 entries)) | 0 / 0, secret scan clean |
| Temporal server + admin-tools 1.32.2 (grype) | 0 / 0 |
| Temporal UI 2.58.0 | 1 High residual, upstream; a locally derived patched image was proven 0/0 but upstream has not republished — operator/GA gate |
| Unreal template payload | 0 / 0 (rebuilt in template); the optional upstream release binary (Go 1.27.0) remains inherited risk only if an operator installs it outside the template |
| Dev-only PostgreSQL 18.6-alpine (local Temporal stack, not shipped) | 8 Critical / 17 High residual, upstream; derived patched image proven 0/0 but not distributable — dev-stack GA gate |

Scanner versions / database dates: govulncheck 1.9.0; npm audit 11.20.0;
pip-audit 2.10.1; grype 0.121.1 (DB 2026-11-05T06:21:06Z); gitleaks 8.30.1.
Residual-risk record and scanner evidence:
`release/qualification/evidence/v1.0.0-rc.1/security/` (source-review,
security-scan, template-build JSON); the Cube template candidate identity and
build evidence are in `template-build-1.0.0-rc.1.json`.

The rebuilt `envd`/Unreal payloads were validated on the local Cube stack
(template `tpl-4fedc58c5f9543a39d721e37`, `cube-smoke`, `factory doctor`,
`agents verify`, zero sandbox leaks) before acceptance.

### Residual inherited-platform risks (not ESF product artifacts)

| Item | Finding | Reachability | Owner | GA gate |
| --- | --- | --- | --- | --- |
| Temporal UI 2.58.0 (upstream image) | 1 High (`GO-2026-5026`) | Dev/reference-stack UI only; not built or published by ESF | Operator deployment (Temporal) | Pin an upstream release that fixes it; a locally derived patched image already scans 0/0 but upstream has not republished |
| PostgreSQL 18.6-alpine (dev-only local Temporal database) | 8 Critical / 17 High in the upstream Alpine layer | Dev/reference-stack database only; never shipped | Operator / development environment | Pin a fixed upstream image or apply the proven derived image outside the release tree |
| `diskcache` 5.6.3 (optional Python closure via dspy) | 1 Medium, unfixed upstream | Both DSPy entry points disable the on-disk cache, so the pickle path is not reachable | ESF dependency tracking | Adopt the upstream fix when released; the CI locked-closure audit keeps a narrow documented exception |
| Cube template arm64 | Not built or scanned; the recipe gates `TARGETARCH=amd64` | Only the amd64 template is shipped/validated | ESF release engineering | Build, scan and qualify an arm64 template before claiming multi-arch template support |
| Optional upstream Unreal release binary (Go 1.27.0) | 8 High in the upstream GitHub release | Only if an operator installs the upstream binary outside the ESF template; the in-template payload is rebuilt and scans 0/0 | Operator | Prefer the template payload; rebuild or publish a fixed install-path binary before offering the upstream release as a supported option |
| Per-image SBOM and vulnerability qualification, transitive-license review | Not automated as release gates | Detection and evidence exist for this release candidate, but no publish-time gate consumes the SBOMs | ESF release engineering | Add SBOM/vulnerability and license gates to the image release workflows for GA |

## Toolchain and component status

Go **1.27.3** and Node.js **26.13.0** remain the required build toolchains;
Python 3.15.2 and uv 0.13.7 remain optional tooling. The execution fabric is
CubeSandbox **0.7.3** (Go SDK v0.7.3). The Temporal family pins are recorded in
the frozen release inventory (1.32.2 server and admin tools, UI 2.58.0, Go SDK
1.50.0 and Go API 1.64.1 at the time of writing). Packaged agents and images
keep their 0.7.3 pins unless the security review changed a digest.

The frozen [`release/inventory.json`](../release/inventory.json) is authoritative
for every exact pin: Go modules, npm and Python graphs, agent archives and
binaries, base-image and service-image digests, the Cube template recipe and
qualification state. It no longer contains a Harness2 dependency entry. Any
security-driven digest change is reflected there and in the security evidence;
this document does not duplicate the full digest list.

## Upgrade notes

1. **Back up and drain first.** Follow the existing upgrade procedure: stop new
   submissions, wait for active runs to reach a terminal state, reconcile
   sandbox cleanup, back up Temporal, the complete artifact tree and the payload
   keyring, then stop `factory-worker`.
2. **Apply the configuration and automation removals from the migration
   section.** A retained 0.7.3 `factory.toml` with Harness2 sections fails
   validation; a retained automation flag fails at argument parsing.
3. **Install the 1.0.0-rc.1 archive or image** for the exact commit to be run.
   The retained `factory.toml` keys, workflow contract, evidence layout and
   result schema are compatible; evidence written by earlier releases is read
   as-is. No schema migration step is required.
4. **Validate before starting:** `factory config validate`,
   `factory doctor --profile production`, `factory agents verify`. Remove the
   improvement deployment overlay and any gateway route from the host.
5. **Restart the worker** and run a disposable conformance run plus
   `factory sandboxes` (expect zero leaked sandboxes) before accepting work.
6. **Replay boundary:** drain active runs before upgrading; rolling-upgrade
   replay against production histories is not qualified in this release candidate. Removed
   improvement workflows cannot be replayed at all.
7. **Release-candidate caveat:** 1.0.0-rc.1 is a production-oriented candidate, not a
   production qualification. Evaluate it in a non-production environment first
   and track the GA gates below. The tag is a prerelease; do not present it as
   the final 1.0.0 release.

## Validation record

Executed on 2026-11-05 against candidate
`1c98bc34dc1a81a1a64e563ffe12bc13549e5442` on the security-refreshed Cube
template `tpl-4fedc58c5f9543a39d721e37` (template identity by ID and image
digest). The evidence-bound record is
[the 1.0.0-rc.1 qualification record](../release/qualification/v1.0.0-rc.1.md)
with artifacts under `release/qualification/evidence/v1.0.0-rc.1/` (security
evidence is separated from build, Cube and acceptance evidence).

- Host compile plus the complete Go suite executed inside disposable Cube
  microVMs: 15 compiled test binaries, 692 passing test records, 0 failures and
  0 skips, both release checks, a clean gitleaks 8.30.1 secret scan, verified
  cleanup and zero owned sandboxes remaining. The packed source archive sha256 is
  recorded in the qualification record.
- Console tests/build (17/17, tracked `dist` unchanged), Python tooling tests
  (including the repaired Docker fallback, 8+7), Temporal schema check,
  `make lint`, release-inventory and Pi-inventory checks, and SBOM validation
  (CycloneDX 1.6, 328 components, no Harness2) passed.
- Conformance acceptance run `run-44042c2526941b3e1bfc70f5` on isolated
  Temporal task queue `esf-rc1-validation-20261105` recorded
  `factory_version = 1.0.0`, passed every deterministic gate with a verified
  patch, and left zero live sandboxes. `factory config validate`,
  `factory doctor`, `factory agents verify` and `make cube-smoke` passed on the
  new template.
- Release-candidate assets were built from a pristine worktree at the candidate
  commit as `v1.0.0-rc.1`; the manifest records commit `1c98bc34`, release
  `v1.0.0-rc.1` and binary release `v1.0.0`. `scripts/verify-release.sh` passed,
  archives contained no Harness2 paths, the CycloneDX 1.6 SBOM is valid, and a
  second build reproduced byte-for-byte.
- Security validation is summarized above: zero Critical/High on ESF product
  artifacts, with inherited platform residuals recorded with owner and GA gate.

This record covers a release candidate. It does not satisfy the GA gates below.

## Outstanding GA gates

The following are explicitly deferred from this release candidate and must close before
1.0.0 is described as production-qualified:

- **24-hour soak** at the intended concurrency, with leak, cleanup, latency and
  resource-growth evidence.
- **Kubernetes/Helm qualification:** installation, upgrade and rollback of the
  Helm chart, worker lifecycle, and reconciliation behavior on the supported
  Kubernetes versions.
- **Restore/replay drill:** isolated restore of Temporal and the artifact tree
  with the matching keyring, resume of a deliberately interrupted run, and
  replay of retained production histories (including the rolling-upgrade case).
- **Hosted-provider and model-backed acceptance:** acceptance on HTTPS
  CubeEgress profiles with the hosted providers and models documented for each
  harness; the release-candidate validation uses deterministic/conformance execution only.
- **Image/template publication:** building, attesting and publishing immutable
  factory/intake images and the Cube template with recorded digests, qualifying
  the published template, and consuming per-image SBOM/vulnerability and
  transitive-license evidence in the release workflow.
- **Performance/load check** at the intended deployment size, and sign-off of
  the residual inherited-platform risk record.
- **Final release review:** an independent review of the final 1.0.0 release
  diff. The release candidate review is complete and recorded in
  [the review record](review-v1.0.0-rc.1.md).

These gates belong to the 1.0.0 GA milestone, not to this release candidate. A passing release candidate
build does not imply any of them has passed.

## Historical records

Historical release notes, security reports and qualification records retain the
pins and statements they recorded, including Harness2 references for the
versions that shipped it. The 0.7.3 record is at
[release-v0.7.3.md](release-v0.7.3.md); the 2026-11-04 component refresh record
is at
[component-upgrade-2026-11-04.md](../release/qualification/component-upgrade-2026-11-04.md).
