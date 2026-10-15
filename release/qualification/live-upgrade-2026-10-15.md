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
| Node template runtime | 24.21.0 | 26.12.0 |
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
- [Postmigration acceptance](evidence/live-upgrade-2026-10-15/postmigration-acceptance.json)
- [Verified patch](evidence/live-upgrade-2026-10-15/conformance-postmigration.log)
- [Rejected no-op patch](evidence/live-upgrade-2026-10-15/noop-postmigration.log)
- [SDK replay](evidence/live-upgrade-2026-10-15/temporal-replay.log)
- [Evidence checksums](evidence/live-upgrade-2026-10-15/checksums.json)

SDK replay covers all three closed histories in full. Both running histories
replay through their committed decisions at event 70. Later timer/cancellation
events have no completed workflow task yet, so their suffixes are retained but
are not claimed as fully replayed. A new deterministic conformance workflow
produced a verified patch on the migrated stack, and a no-op agent was rejected
by the mandatory unit-test gate. Both runs verified zero live VM leaks. Their
egress probes were inconclusive, so these runs do not pass the security gate.

The first CI run also exposed a separate Pi test timing failure: a 900 ms
deadline could expire before the first provider request on a busy runner.
The revised test uses a 10 s budget, crashes after 6 s, verifies a second engine
request and rejects completion after 13 s. A reset deadline would exceed that
bound. Its process inventory is controlled independently of the real surviving
process tests. All 17 runner scenarios passed inside Cube:
[Pi deadline regression](evidence/live-upgrade-2026-10-15/pi-deadline-regression.log).

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

Status: **passed for the local Linux/amd64 Cube VM profile**. Claude Code
2.1.300 and Codex 0.162.1 ran as the actual native clients with
`deepseek-v4.1-flash` through OpenCode Go. The factory binary used source
`886a675ab9c894f202e076ea9a741d872830143a`; later changes in this PR add operator
routing support, regression checks and retained evidence.

| Case | Claude | Codex |
| --- | --- | --- |
| Implement greeting change; unchanged gates pass | passed | passed |
| Successful agent leaves failing baseline; mandatory gate rejects it | passed | passed |
| Review pauses with expected patch; explicit fixture approval resumes it | passed | passed |
| Active process/file credential scan and five network boundary checks | passed | passed |

All eight workflows completed with their expected outcomes and verified
cleanup. The review approvals were operator commands on disposable fixtures;
they do not represent independent PR review or production acceptance.

The real Go credential was absent from 186 retained artifact files, all eight
workflow histories and 322 decoded Temporal payloads. Live snapshots observed
both native CLI processes and their placeholder credentials; the real key was
absent from their process environments, command lines and scanned mutable
workspace/authentication files. Raw snapshots and histories remain private.
These observations cover those surfaces, not arbitrary VM memory.

For each active CLI, unapproved paths, the other client's protocol endpoint,
and a Host/SNI mismatch returned HTTP 403. Direct public and metadata access
failed with connection refusal. Separate protocol probes returned HTTP 200,
the requested model ID and nonzero usage for all three API formats. Native
completion events also retained usage. Client cost estimates are not treated
as reconciled Go subscription charges.

- [Native workflows and credential boundaries](evidence/live-upgrade-2026-10-15/native-provider-qualification.json)
- [Protocol responses after persistent routing activation](evidence/live-upgrade-2026-10-15/native-protocols.json)
- [Routing qualification](evidence/live-upgrade-2026-10-15/cube-routing-qualification.json)
- [Sanitized proxy audit](evidence/live-upgrade-2026-10-15/native-proxy-audit.json)

Two host interactions were resolved: Calico cleared Cube's original packet
mark bits, and unscoped mark routing conflicted with reverse-path validation.
The operator configured separate mark bits and the supported network-init
override to scope only Cube's table-100 rules to its ingress interface. Two
activation cycles passed, with all non-Cube routing rules and VPN sysctls
preserved. The earlier
[pre-fix probe](evidence/live-upgrade-2026-10-15/provider-protocol-beforemarks.log)
is retained as failure evidence. See [local routing](../../docs/cube-local-routing.md).

The factory's legacy pre-agent canary still reports `UNKNOWN`; the explicit
live checks above are separate evidence and do not rewrite that condition.
Kubernetes, non-root enforcement, full image/security qualification, performance
and soak gates remain outside this local native-client qualification.
