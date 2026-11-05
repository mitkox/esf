# ADR 0009 — Remove the Harness2 improvement proof of concept

- **Status:** Accepted
- **Date:** 2026-11-05
- **Deciders:** ESF maintainers
- **Relates to:** [ADR 0006](0006-control-plane-security-boundary.md) (control-plane
  security boundary), [ADR 0007](0007-defense-in-depth.md) (defense in depth).
  Historical release records that mention Harness2 remain valid for the versions
  they describe.

## Context

The 0.7.x line carried an experimental implementation of the Harness2
Propose–Probe–Compose method: Go/Temporal improvement workflows, immutable
instructional profiles, offline experiment suites, a loopback `budget-gateway`
model route with a durable budget ledger, signed promotion receipts, and pinned
upstream provenance under `third_party/harness2/`. Only ESF-native code and
adapted role instructions were used; no upstream Harness2 runtime, installer or
Python runner was ever a dependency or shipped artifact.

The feature never left candidate status. The release inventory recorded
`production_qualified: false`, and no approved profile, model-backed quality
evidence, pricing attestation, Kubernetes or soak qualification, or production
acceptance existed. The 2026-10-19 integration record qualified only the
deterministic implementation; it was explicitly not a quality or production
claim.

Keeping it in 1.0.0 would carry a second, unqualified execution engine with:

- its own CLI commands, flags, configuration sections and reviewed-apply fields;
- its own credential path (gateway control/token secrets, upstream provider key)
  and durable budget state that need security review, runbooks and pricing
  validation;
- additional Temporal workflow types, activities and failure modes beyond the
  single-run factory contract;
- a third-party adaptation surface, notice obligations and an extra qualification
  axis in release engineering;
- continuous maintenance and operator support the 1.0.0 scope does not fund.

The removal is a breaking change, but it is cheapest before 1.0.0: no stable
release has promised the surface, no operator data must be migrated because the
factory reads only the configuration and workflow types it registers, and the
retained product (runs, verification, review, console, harness adapters) is
unchanged.

## Decision

Remove the Harness2 proof of concept from ESF in full before 1.0.0:

- Go packages and wiring: `internal/harnessprofile/`, `internal/inferencebudget/`
  (the gateway/ledger accounting used only by the removed commands), the
  improvement files and coupling in `internal/factory/`,
  `cmd/factory/improvement.go` and related tests;
- CLI: `factory improve`, `factory profile` (with its `import`, `inspect`,
  `qualify`, `promote` and `approval-payload` subcommands) and
  `factory budget-gateway` (including `budget-gateway digest`); the
  `--harness-profile` and `--improvement-policy` run flags, and the
  `harness_profile` reviewed-manifest field;
- configuration: `[harness_profiles]`, `[improvement]`, `[improvement_policies]`
  and `[improvement_suites]`, plus `scopes.<name>.harness_profiles` and
  `scopes.<name>.improvement_policies`;
- deployment and provenance: `deploy/harness-improvement/`,
  `third_party/harness2/`, and the Harness2 license/notice files previously
  copied into release archives;
- documentation and qualification: `docs/harness-improvement.md` and the
  2026-10-19 Harness2 integration qualification record with its evidence.
  Other historical records keep their original Harness2 references and pins.

No replacement improvement pipeline is introduced. Ordinary factory runs,
verification and review gates, the console, the sf turn service and the existing
harness adapters keep their behavior and contracts.

## Consequences

- Breaking CLI and configuration change relative to 0.7.3. Configuration decoding
  rejects unknown fields, so a `factory.toml` that still carries the removed
  sections fails validation loudly; operators must delete them before upgrading.
  Removed commands and flags fail instead of silently doing nothing.
- In-flight improvement experiments cannot resume: their workflow types and
  workers are no longer registered. Drain or cancel them before upgrading.
- The shipped surface shrinks: fewer packages, workflow types, activities,
  configuration keys, credentials, ports, third-party notices, documents and
  qualification axes to review and maintain.
- Historical records and release notes are retained rather than rewritten, so
  they still describe the removed candidate for the versions that shipped it.
  The 2026-10-19 Harness2 qualification record is removed with the subsystem it
  qualified because it is not evidence for anything ESF 1.0.0 ships.
- No upstream Harness2 runtime is affected. ESF never shipped the upstream
  Python runner, installers or process launchers, so removing the adapted
  instructions and provenance removes no operator runtime dependency.
- If improvement work returns, it does so as a separately designed and
  qualified feature with its own ADR, threat model and acceptance evidence.
