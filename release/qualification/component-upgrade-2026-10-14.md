# ESF 0.6.2 component upgrade validation

Upstream versions were checked on 14 October 2026. Local validation completed
on 15 October in Europe/Sofia. See the [candidate component table](../../docs/release-v0.6.2.md).

## Failure and correction

[PR #41's original CI run](https://github.com/mitkox/esf/actions/runs/38633303661)
failed the Linux Go vulnerability scan with 13 reachable advisories. macOS
passed; later Linux tests were skipped. Go 1.27.2 and x/net 0.61.0 replace the
vulnerable pins. No findings or checks are suppressed.

Stable releases were resolved from official GitHub releases, npm and PyPI
metadata, Go download metadata, and Docker registry manifests. Lockfiles,
package integrity, immutable image digests, bundled artifacts, and embedded
inventories are synchronized. Machinist's retained fork revision already
contains upstream v0.4.0; Unreal and Codex are also already current.

OpenCode's portable `@opencode/cli-linux-x64-baseline` artifact is selected
explicitly, avoiding a CPU-dependent postinstall selection or the package's
uninstalled launcher stub. Node 26's libatomic dependency is included in both
Cube recipes. PostgreSQL 18 uses its parent-directory volume layout and an
explicit new volume selection; an old environment without that selection
fails before startup. Existing databases were not migrated during validation.

## Local results

Project builds and tests executed in disposable Cube microVMs. The host built
and drove the factory and validation drivers. The running infrastructure was
CubeSandbox 0.7.2 with its existing READY template and Temporal 1.32.0. The
latest Cube Go SDK successfully created, executed in, and destroyed VMs on
that deployment.

- Go 1.27.2: `go vet ./...`, `govulncheck` 1.9.0 with a fresh official database
  snapshot, and `go test -race -count=1 ./...` passed. No vulnerabilities found.
  GCC and libc headers were installed inside the test VM for race compilation.
- Node 26.12.0: Pi 1.2.0 bundle/catalog generation and 17 runner tests passed;
  frontend build and 40 tests passed. Generated bundles, licenses and hashes
  were retained. npm audits of all three locked graphs reported zero findings.
- Python 3.15.0: fresh `uv sync --locked` installed both local tools and DSPy
  3.4.1. Intake 8, brief lab 7, evals 71, and qualification-boundary 1 tests
  passed. Native source builds needed GCC on the older Ubuntu test template.
- Actual CLI startup: Claude Code 2.1.300, Codex 0.162.1 and portable OpenCode
  2.0.29 passed. Secret scan, issue-triage tests, and both inventory verifiers
  passed.
- Rebuilt factory conformance `run-3be69ef5368834d855a27e0c` succeeded on an
  isolated task queue: verified 283-byte patch, passing build/test gates and
  verified cleanup. Its development egress profile allowed public internet;
  the metadata probe was inconclusive, so this is not a security qualification.
- Every disposable validation VM was destroyed. Final live sandbox count: 0.

The primary checkout's unrelated edits and local configuration were preserved.
Sanitized local evidence is retained under `.factory/component-upgrade-20261015/`.
The validated source archive hash is `fad6cdf64514b48f285bb4136a047b9c32bdfd369a2b63c26f85d21a0ddafc66`;
that archive predates this evidence note and the README link update.

| Retained log | SHA-256 |
| --- | --- |
| `go-race.log` | `c63cd00593d5e2c4da0d006061d71054c2ae993a19e27242cc9930f684d76c87` |
| `go-vulnerabilities.log` | `3016e51e4eac0d421674d2128bbbdefb2924b4646e0c14a1ab034977ad73fae5` |
| `pi-upgrade-tests.log` | `cac505ca9b0d410d5a61573312bd9b73ecdff62e839e55b69884d0943c2fadbe` |
| `frontend-upgrade-tests.log` | `9f2e13c2b910954a9cb781113cd0ef6c7e7e8e042df5e453ca2b96bd698c6939` |
| `python-tests-final.log` | `50d0f8f83ce0c6b94a06ef4a2875d7915918ae775b1dbb78c1069b4feac6fe80` |
| `release-final.log` | `a441e70ec5c961f9ed94329d8aa75e8d21f8ff4dbf257094184aa8f09bd6af58` |
| `native-cli-startup.log` | `3c546230cdaf37106f270cbf3e7760a16ff3100d41a2c3bea1cbe6993ba193c1` |

## Follow-up compatibility regressions

The first upgrade CI run rebuilt the exact committed Pi bundle but failed the
restart-allowance test while waiting for its second engine. That test used the
real global process inventory, so unrelated processes starting on a shared
runner could correctly prevent recovery. Journal corruption and restart-budget
cases now inject a deterministic inventory. The dedicated surviving-process
and uncertain-inspection tests continue to check fail-closed recovery.

The official Cube 0.7.3 create schema still accepts the snake_case denial flag,
while its Go SDK emits camelCase. Explicit denial compatibility now covers
0.7.2 and 0.7.3, including tagged versions. Defaults, allow requests, unknown
versions and future schemas retain their existing handling. A negative test
uses the old implementation to demonstrate that the new 0.7.3 denial cases
fail before the fix; corrected Cube/factory/agent/CLI race suites then pass. The full Pi suite passed
again, and the repaired recovery case passed five additional consecutive runs.
Both validation VMs were destroyed; final sandbox count was zero.

| Follow-up log | SHA-256 |
| --- | --- |
| `cube-denial-negative.log` | `99bdb60e939ae06f700ca2502f7b799bcf9f246746beb3e3604c682bbb2ba6e6` |
| `cube-denial-race.log` | `acf8c2e49d879b6832a87e6f78de1a0e217326f206fde500f8212a42514a21fd` |
| `cube-release-pins.log` | `83eec82092dc5f58cc9890dc2e664e6f7312fb882a795356f76515668dcc531f` |
| `pi-recovery-regression.log` | `6b6314ed2a82d05acea62e063e54aec86f4621cb6bf9b874cb5cfb54db92684a` |

The follow-up source archive hash is `35b822267afb3f7babc91a020960b77cd69650bac6966c8599e5130c19d3fd2d`.

## Qualification boundary

These are source and local compatibility results. They do not establish a
Cube 0.7.3 or Temporal 1.32.1 rollout, PostgreSQL 18 migration, rebuilt template
qualification, hosted-provider completion/header injection, Kubernetes,
restore, soak, or production acceptance. Qualification fields remain pending
for the updated execution chain. Hosted CI status is available on PR #41;
release tags and production deployment are separate from this source update.
