# Local service upgrade and PostgreSQL 18 migration

Execution source: `076edba` (ESF 0.6.2), Linux/amd64, local CubeSandbox.
This record covers the local service migration. It does not pass the broader
VM/Kubernetes production qualification matrix.

| Component | Previous live version | Active version |
| --- | --- | --- |
| CubeSandbox | 0.7.2 | 0.7.3 |
| PostgreSQL | 16.15 | 18.6 |
| Temporal server / admin tools | 1.32.0 | 1.32.1 |
| Temporal UI | 2.55.0 | 2.56.0 |
| OpenCode template binary | 2.0.24 | 2.0.29 |
| Pi Durable / Pi AI / Chord | 1.0.4 | 1.2.0 |
| Node template runtime | 26.10.0 | 26.12.0 |
| Claude Code | 2.1.273 | 2.1.300 |
| Codex | 0.162.1 | 0.162.1 |

## Migration evidence

The final migration drained Temporal, backed up both databases with the
PostgreSQL 18 client, and restored them into a separate volume mounted at
`/var/lib/postgresql`. Every public table's row count and digest of its sorted
row contents matched before schema updates or server startup. Schema versions
remain 1.19 for persistence and 1.14 for visibility.

Temporal 1.32.1 served the same five user workflow identities, statuses and
complete history digests, including both running workflows. The PostgreSQL 16
rollback volume was retained. A rollback stack without the active project's
Compose labels served those same five histories; it was then stopped.

Database dumps, environment files, Cube installation backups and its metadata
backup remain owner-only local artifacts. Public evidence contains only row
counts, digests, versions and verification results:

- [Final migration](evidence/live-upgrade-2026-10-15/postgresql-live-migration.json)
- [Rollback drill](evidence/live-upgrade-2026-10-15/rollback-drill.json)
- [Schema safety: six scenarios](evidence/live-upgrade-2026-10-15/schema-safety.log)
- [Previous script fails the regression](evidence/live-upgrade-2026-10-15/schema-safety-negative.log)
- [Evidence checksums](evidence/live-upgrade-2026-10-15/checksums.json)

## Template

An operator-built image contains the pinned OpenCode, Unreal, Pi, Node,
Claude and Codex binaries without credentials. Docker builds checked their
digests. Startup versions and digests were measured in a disposable Cube VM:
[template measurements](evidence/live-upgrade-2026-10-15/native-template-versions.log).

- Image: `localhost:32000/esf-native@sha256:41dd09763771ad58732adc2413062e010a917770f473a0ea160bc6121c85b614`
- Template: `tpl-e0b123805849416a915a751e`, READY
- Platform: Linux/amd64
- CubeEgress public root CA baked by Cube's template builder
- Credentials excluded from the image and VM provisioning

## Provider qualification status

Requested: Claude and Codex CLIs using `deepseek-v4.1-flash` through the
operator's OpenCode Go subscription, with its key held outside the VM and
injected by CubeEgress.

Status: **pending**. Restricted HTTPS probes currently fail before reaching
the provider. Calico's raw PREROUTING rules clear bits used by Cube's default
HTTP/HTTPS packet marks, and both Cube TPROXY counters remain zero. The
[pre-fix probe](evidence/live-upgrade-2026-10-15/provider-protocol-beforemarks.log)
records connection refusal, not successful inference.

The operator-owned mark correction is described in
[local routing](../../docs/cube-local-routing.md). Native provider acceptance,
new workflow execution, historical SDK replay and credential-isolation
qualification remain pending until that correction is applied and measured.
