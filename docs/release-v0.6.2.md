# ESF 0.6.2 component and agent integration candidate

0.6.2 refreshes ESF components to the latest stable upstream releases checked
on 14 October 2026 and adds opt-in Claude Code and Codex CLI harnesses.
OpenCode remains the automatic default.

The table below records the 14 October checkpoint that opened this candidate.
The current pinned inputs are recorded in `release/inventory.json`; the later
refresh is recorded in the
[27 October component record](../release/qualification/component-upgrade-2026-10-27.md).

| Component | Previous pin | Candidate pin |
| --- | --- | --- |
| Go | 1.27.1 | 1.27.2 |
| Node | 24.21.0 | 26.12.0 |
| Python optional tools | 3.14.8 | 3.15.0 |
| uv installer | 0.12.23 | 0.13.1 |
| CubeSandbox and Go SDK | 0.7.2 / SDK snapshot | 0.7.3 / v0.7.3 |
| Temporal server / admin tools | 1.32.0 | 1.32.1 |
| Temporal UI / API | 2.55.0 / 1.63.6 | 2.56.0 / 1.63.7 |
| PostgreSQL recipe | 16.15 | 18.6 |
| OpenCode V2 | 2.0.24 | 2.0.29 |
| Pi Durable / Pi AI / Chord | 1.0.4 | 1.2.0 |
| Claude Code tested CLI | 2.1.273 | 2.1.300 |
| DSPy | 3.4.0 | 3.4.1 |
| Kubernetes qualification target | 1.36.2 | 1.37.1 |
| Go vulnerability scanner | 1.8.0 | 1.9.0 |

Codex 0.162.1, Unreal 0.3.1, Temporal SDK 1.49.0, gRPC 1.84.0,
OpenTelemetry 1.47.0, Helm 4.3.0, and gitleaks 8.30.1 are already the latest
stable versions.

Go 1.27.2 and x/net 0.61.0 address the 13 reachable advisories that failed
PR #41's Linux vulnerability check. The checks remain enabled. Go and Python
transitive dependencies, npm lockfiles, embedded release metadata, base-image
digests, and GitHub Action commit pins are refreshed together. Pi's bundle,
license notices and model catalog are rebuilt in Cube.
Node 26 requires libatomic1; both Cube template recipes install it. CubeAPI
0.7.3 still reads `allow_internet_access` on create, while its Go SDK sends
camelCase. The existing explicit-denial compatibility rule now covers both
0.7.2 and 0.7.3; the operator example records the selected Cube version.

## Removed: Machinist

0.6.2 removes the inherited Machinist product surface: the `machinist` CLI,
local control plane, embedded web UI/console, managed worker, SQLite store and
schema migrations, updater, GitHub/cron triggers, the `agent.py` issue-to-PR
workflow, examples, skill and Machinist documentation. The `factory` CLI is the
only supported control plane, and ESF now ships one factory archive per platform
instead of factory/machinist/combined archives. The `console` and
`managedworker` container images are discontinued, and the Helm chart no longer
deploys a console. The intake and Cube-template images remain part of the
release image set.

The removal is recorded in [ADR 0008](adr/0008-remove-machinist.md). The factory
had no code dependency on Machinist; the inherited model executed agents on the
worker host outside Cube isolation. The UI and managed queue were not on the
factory execution path and were used only for a few demonstration jobs.
Removing them shrinks the release, CI, documentation, qualification and security
surface.

Migration:

- stop and disable `machinist-control-plane`/`machinist-worker` units;
- on Kubernetes, stop the old console and export/snapshot its console PVC (or
  `console.storage.existingClaim`) before upgrading; PVC deletion is
  irreversible. Then remove `console` values and delete any console Deployment
  or Secret that your installation manages outside the chart (`helm upgrade`
  removes resources created by earlier chart versions);
- on a host install, archive or delete the Machinist service user's state,
  normally `/home/machinist/.machinist`; the factory does not read it;
- replace `machinist` commands with the `factory` equivalents and inspect
  evidence with `factory describe`, `status --watch`, `logs` and
  `get changes`;
- `ESF_COMPONENT=machinist|combined` now fails with guidance; install the
  default factory archive.

The console's read-only factory evidence view has no replacement in this
release. Temporal's UI remains available for workflow state; a small
factory-only evidence viewer remains possible future work.

## Claude and Codex

Use `factory run ... --agent claude` or `--agent codex` after registering the
harness and provisioning its standalone binary or complete template runtime.
See the [operator guide](harness-claude-codex.md).

Both factory harnesses require CubeEgress credentials. Real keys stay in the
trusted proxy; only placeholders enter the Cube microVM. Credential injection
is restricted to HTTPS POST inference and token-count/compaction endpoints.
Environment credential mode, custom argv, arbitrary environment passthrough,
and host authentication/session files are rejected. Provider policy errors are
redacted before they enter tracing or Temporal history, including rotated keys.
The optional CLI binaries are operator-provisioned and are not bundled into
the ESF distribution.

## Deployment and validation boundary

The updated production recipes are candidates. The local development services
have moved to Cube 0.7.3, Temporal 1.32.1, UI 2.56.0 and PostgreSQL 18.6, with
table-content and complete workflow-history checks plus a rollback drill.
See the [local upgrade record](../release/qualification/live-upgrade-2026-10-15.md)
for template measurements and the current provider qualification status.
Production qualification requires evidence for this execution chain; earlier
results do not qualify new component versions.

PostgreSQL 18 changes the Docker volume layout. The development recipe mounts
`/var/lib/postgresql` and uses a separate volume named with `-v18`. Before
changing an existing stack, stop submissions, drain workers, back up both
Temporal databases and namespace/configuration state, and restore into an
isolated PostgreSQL 18 stack. Apply Temporal's schema upgrades and verify
history replay, workflow completion, and restore/rollback before switching.
Retain the PostgreSQL 16 volume and matching old configuration for recovery.
See [the Temporal upgrade notes](../deployments/dev/temporal/README.md).

The [existing qualification procedure](release-v0.6.0.md) applies to the exact
0.6.2 commit and assets, except that console and managed-worker steps in older
procedures no longer apply. The 0.6.2 binary asset set is factory-only: four
`esf_factory_*` archives. The intake and Cube-template images continue to be
built and qualified separately. Hosted-provider inference, live provider
header-injection qualification, and the production deployment matrix remain
separate gates. This source version bump does not publish a release tag or
qualify production.
