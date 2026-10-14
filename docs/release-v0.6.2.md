# ESF 0.6.2 agent integration candidate

0.6.2 adds opt-in Claude Code and Codex CLI harnesses. OpenCode remains the
automatic default, and the 0.6.1 dependency pins are retained.

Use `factory run ... --agent claude` or `--agent codex` after registering the
harness and provisioning its standalone binary or complete template runtime.
See the [operator guide](harness-claude-codex.md).

Both factory harnesses require CubeEgress credentials. Real keys stay in the
trusted proxy; only placeholders enter the Cube microVM. Credential injection
is restricted to HTTPS POST inference and token-count/compaction endpoints.
Environment credential mode, custom argv, arbitrary environment passthrough,
and host authentication/session files are rejected. Provider policy errors are
redacted before they enter tracing or Temporal history, including rotated keys.

Release metadata is synchronized across factory/Machinist builds, Python and
its lockfile, Helm, Docker defaults, and both release inventories. No existing
component versions are upgraded by this candidate.

Validation uses the local CubeSandbox stack: race-enabled adapter/factory/CLI
tests, credential-boundary regressions, release inventory checks, actual CLI
startup, and deterministic factory conformance acceptance. The tested optional
CLI versions are recorded in the release inventory; their binaries are
operator-provisioned and are not bundled into the ESF distribution.

The [existing qualification procedure](release-v0.6.0.md) applies to the exact
0.6.2 commit and assets. Hosted-provider inference, live provider header-injection
qualification, and the production deployment matrix remain separate gates.
This source version bump does not publish a release tag or qualify production.
