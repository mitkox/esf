# Component and security refresh — 2026-10-25

This candidate starts at `804fb461068972a707628b0a3243e4db09cafb0c`.
It refreshes the ESF 0.6.2 source and deployment recipes. It is not production
qualification, a live infrastructure upgrade, or a security sign-off.
The [implementation plan](../../docs/plans/2026-10-25-component-security-upgrade.md)
records the scope and acceptance criteria.

## Component disposition

Versions were read from official release APIs, Go module proxy, npm, PyPI,
Python.org, and container registry manifests on 2026-10-25. Package integrity,
release-asset checksums, and image manifest digests were checked before use.

| Component | Baseline | Candidate |
| --- | --- | --- |
| Go | 1.27.2 | 1.27.3 |
| Node.js | 26.12.0 | 26.12.1 |
| Python | 3.15.0 | 3.15.1 |
| uv | 0.13.1 | 0.13.5 |
| Temporal Go SDK / API | 1.49.0 / 1.63.7 | 1.50.0 / 1.64.0 |
| Temporal UI | 2.56.0 | 2.57.0 |
| SQLite | 1.60.1 | 1.61.0 |
| OpenCode | 2.0.29 | 2.1.4 |
| Pi Durable / Pi AI / Chord | 1.2.0 | 1.4.0 |
| Optional Claude / Codex CLIs | 2.1.300 / 0.162.1 | 2.1.310 / 0.164.0 |
| Vite / Lucide / jsdom | 8.3.4 / 1.57.0 / 30.1.2 | 8.3.5 / 1.61.0 / 30.1.3 |
| Helm qualification tool | 4.3.0 | 4.3.1 |
| docker/login-action | 4.6.0 | 4.7.0, pinned commit |
| certifi / filelock | 2026.7.22 / 3.32.7 | 2026.10.23 / 4.2.4 |

Other maintained Go, npm, and Python dependencies were checked or re-resolved
with the locked graphs. CubeSandbox/SDK 0.7.3, Unreal 0.3.1, Temporal server and
admin tools 1.32.1, PostgreSQL 18.6, OpenTelemetry 1.47.0, gRPC 1.84.0, React
19.3.1, Tailwind 4.3.3, DSPy 3.4.1, Kubernetes 1.37.1, govulncheck 1.9.0, and
gitleaks 8.30.1 remain current stable pins. PostgreSQL 19 is a release candidate.
The adopted Harness2 commit still matches upstream HEAD. Machinist is the
existing source-derived integration, rather than a replaceable package runtime.
Other Action release/tag identities were checked and retained.

Go, Node, Python, Debian 13, distroless, Temporal UI, and PostgreSQL image
identities were refreshed. Cube's current published base digest is unchanged;
versioned 0.7.3 base tags are not published. The release inventory retains exact
digests and records image/template qualification as pending.

LiteLLM 1.103.9 is the resolver's compatible Python <3.15 selection. The newest
1.106.3 requires `filelock<4`, which conflicts with the repaired security floor.
No override bypasses upstream dependency constraints. Python 3.15 does not
install DSPy's conditional LiteLLM dependency.

## Security changes and remaining findings

- Go 1.27.3 contains the upstream certificate trust security fix described in
  the [Go release history](https://go.dev/doc/devel/release#go1.27.3).
- Updated certifi removes the compromised certificate authorities identified by
  [GHSA-6q48-6c7w-9gw2](https://github.com/advisories/GHSA-6q48-6c7w-9gw2).
- Updated filelock repairs the file-reopening advisory
  [GHSA-v4x8-3wr5-3wq8](https://github.com/advisories/GHSA-v4x8-3wr5-3wq8).
  Resolver constraints prevent either package falling below its fixed version.
- `require_non_root` now rejects an unverified UID. Configuration cannot enable
  that requirement while disabling the probe that measures identity. The two
  regression cases failed before the repair and passed afterward in Cube.
- Pi's interrupted-tool test now blocks a FIFO write because upstream 1.4
  refuses nonregular reads. It checks the recovered tool result, not text that
  can also occur in the system prompt, and still requires exactly one restart
  without replaying the unsafe write.

OpenCyber's refreshed deterministic source scan removed the certifi and
filelock findings. It retains two high root-user findings for the Cube template
recipes, one medium `diskcache` advisory, and six low missing-healthcheck
findings. No findings were suppressed.

The Cube findings include actual guest agent privilege: the pinned SDK defaults
commands to root, and its base image's alternative user has passwordless sudo.
Adding Dockerfile `USER` alone would not fix that boundary. Factory, console,
managedworker, and intake images already choose nonroot users. A complete Cube
privilege split still needs explicit command identity, privileged provisioning,
workspace ownership, restricted sudo, and runtime qualification. The optional
UID check is an execution gate; it does not remove guest administration rights.

`diskcache` remains transitive with no scanner-listed fixed release. ESF's
configured Python inference entry points disable disk caching before model use.
This mitigation does not cover direct library calls before configuration,
future early cache use, or other processes using the dependency.

The configured `local/deepseek-v4-flash` AI security provider refused connection.
AI triage remains incomplete; no fallback provider was selected. The scanner
results and valid engagement audit receipts remain independent evidence.
Generated bundles are outside the source watcher's standing scope; npm graph
audits, artifact regeneration, and Cube tests cover them separately.

## Verification and evidence

Candidate verification uses local Cube microVMs. The host only coordinates,
edits source, verifies downloads, and compiles binaries. Detailed command,
source, toolchain, binary, and output hashes are retained in private evidence
under `/tmp/esf-upgrade-evidence-20261025/`; OpenCyber engagements are retained
by the configured security harness.

| Check | Result |
| --- | --- |
| Go race binaries executed in Cube | 23 test-bearing packages passed; 1,138 pass records, 3 expected skips |
| Go vet | Passed in Cube |
| govulncheck 1.9.0 / gitleaks 8.30.1 | No Go vulnerabilities / no detected leaks |
| Frontend tests / production build | 40 tests passed; build passed |
| Pi runner regressions | 17 tests passed, including the strengthened interrupted-write assertion |
| npm audits | All three locked graphs reported zero vulnerabilities |
| Issue-triage tests | 6 tests passed |
| Python 3.15.1 | Intake 8 tests and brief lab 7 tests passed; installed dependency consistency passed |
| Python 3.14.9 conditional graph | The same 15 tests passed; 72 installed packages consistent, including LiteLLM/filelock imports |
| DSPy CLI cache configuration | Both actual configuration paths disable disk caching and retain 1,024 memory entries under a fresh cache directory |
| Existing OpenCode / Unreal installer | Installation and final digests passed; OpenCode version and Unreal's supported help command started successfully |
| Nonroot hardening regressions | Both cases failed before the repair and passed afterward with race detection |

The three Go skips are the Darwin-only process-group test on Linux, the
standalone subprocess-helper entry, and the opt-in disposable GitHub intake
test. They do not represent completed macOS or live GitHub acceptance.

Early failures remain retained: the pre-rebuild Pi version check, the obsolete
FIFO-read recovery fixture, missing native Python build tools, and the selected
template's writable-disk ceiling during Go compilation. An unsupported Unreal
`--version` invocation also failed; its supported help invocation passed.
These failures are not passes. A suspected OpenCode CPU-selection digest
mismatch was disproved: the current optimized and baseline packages contain
the same verified binary, so the existing installer was retained.
The Python native build used explicitly pinned Rust/GCC inside the owned VM;
the intake recipe uses Debian 13, which supports the newer binary wheel's
glibc requirement. Go test binaries may be compiled on the host and executed
in Cube; project tests and linters never run on the host.

Deployment image builds/scans, live model acceptance of the refreshed agents,
Temporal history replay, VM/Kubernetes upgrade drills, restore, soak, and
production performance remain distinct qualification gates. Existing successful
results for earlier agent versions do not transfer to the refreshed binaries.
The inventory's production gates remain pending.
