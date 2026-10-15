# Temporal development environment

The factory's durable orchestration layer runs on [Temporal](https://temporal.io).
This directory contains a **minimal, reproducible, loopback-only** development
stack for it.

## Source and attribution

This configuration is adapted from the **current official reference**:

- Repository: <https://github.com/temporalio/samples-server>
- Directory: `compose/`
- File: `docker-compose-postgres.yml` (+ `.env`, `scripts/`, `dynamicconfig/`)
- Inspected at commit: `f811a033a5e79402cab9f792cea132f50344bd17`

The **obsolete, archived** `temporalio/docker-compose` repository is
deliberately **not** used. Only the minimal supported development configuration
was extracted; the upstream repository is not vendored.

## Local configuration

Copy `.env.example` to `.env` and replace the database password with a random
local value before running `make temporal-up`. Never commit `.env`.

Set `TEMPORAL_SCHEMA_MODE=create` only for empty databases. After restoring an
existing Temporal database and visibility store, set it to `upgrade`. The
upgrade path runs only `update-schema`; it never creates databases or invokes
`setup-schema -v 0.0`. Upstream setup preserves higher existing schema versions;
the explicit mode records operator intent rather than relying on bootstrap
idempotence. A missing or invalid mode
fails before contacting PostgreSQL.

## What runs

| Service | Image | Host binding | Purpose |
| --- | --- | --- | --- |
| `postgresql` | `postgres:18.6-alpine` | `127.0.0.1:5433` | Temporal persistence + visibility store |
| `temporal-admin-tools` | `temporalio/admin-tools:1.32.1` | — | one-shot schema setup |
| `temporal` | `temporalio/server:1.32.1` | `127.0.0.1:7233` | gRPC frontend |
| `temporal-create-namespace` | `temporalio/admin-tools:1.32.1` | — | one-shot namespace creation |
| `temporal-ui` | `temporalio/ui:2.56.0` | `127.0.0.1:8233` | web UI |

### Deliberate deviations from upstream

1. **Every published port binds to `127.0.0.1`.** Upstream publishes `0.0.0.0`.
   The Temporal server must never be reachable off-host, so this stack does not
   expose it beyond loopback.
2. **Host ports are moved off the defaults.** `5432`, `3000` (upstream UI CORS
   target) and `8080` are already occupied on this machine by unrelated
   services. This stack uses `5433` and `8233`.
3. **Exact version pins** instead of a floating major (`POSTGRESQL_VERSION=16`).
4. **No Elasticsearch / OpenSearch.** PostgreSQL alone provides both persistence
   and visibility. The MVP's workflow volume does not need a search cluster, and
   the official PostgreSQL-only compose variant is the supported minimum.
5. **A known upstream typo is corrected** in `scripts/create-namespace.sh`
   (`$MAX_ATTdMPTS` → `$MAX_ATTEMPTS`); with `set -u` the typo aborts the script
   exactly on its error path.

## Usage

```bash
make temporal-up        # start the stack, wait for health
make temporal-status    # show container + namespace status
make temporal-down      # stop the stack (data volume is preserved)
```

Direct compose equivalent:

```bash
cd deployments/dev/temporal
docker compose up -d
docker compose ps
docker compose down          # add -v to also delete the data volume
```

## Endpoints

| Endpoint | Value |
| --- | --- |
| Temporal gRPC | `127.0.0.1:7233` |
| Temporal UI | <http://127.0.0.1:8233> |
| Namespace | `default` |
| PostgreSQL | `127.0.0.1:5433` (`temporal` / `temporal`) |

## Data

Temporal's own state lives in the configured `POSTGRESQL_DATA_VOLUME`
(default `factory-temporal-postgresql-data-v18`) Docker volume, so workflow history survives `make temporal-down`. Because
`restart: unless-stopped` is set, the containers also come back after a host
reboot.

Factory artefacts (patches, logs, evidence) are **not** stored here — they live
in the factory artifact store (`.factory/runs/<run-id>/` by default). Temporal
holds workflow state only.

## Verifying the stack

```bash
make temporal-up
make temporal-status
make temporal-hello        # runs a tagged integration workflow end-to-end
```

`make temporal-hello` is the proof that gRPC is reachable, the namespace exists,
and a workflow can actually execute — not merely that a container is running.

PostgreSQL 18 uses a fresh `factory-temporal-postgresql-data-v18` volume mounted
at `/var/lib/postgresql`. Existing PostgreSQL 16 volumes are preserved. Back up
the Temporal database and visibility store, restore them into the new 18.6
instance, and validate Temporal schema/replay before switching an existing
installation. Do not run `docker compose down -v` during this migration.

The updated compose file requires `POSTGRESQL_DATA_VOLUME` explicitly. An old
`.env` without it fails before startup. For an existing 16.x installation, keep
its old compose file, `.env`, and volume until backup/restore validation passes.
Do not mount the old 16.x volume using the new 18.x directory layout. Use
distinct container names and ports for the isolated migration stack.

Drain Temporal before the final two-database backup. Compare every restored
table's row count and content digest before applying schema upgrades. Retain
the original volume, image digests, private environment file and service
configuration until the new server serves the same workflow identities,
statuses and complete histories and can execute a new verified workflow.
Exercise rollback against the retained PostgreSQL 16 store before accepting
the migration. After new workflows write to PostgreSQL 18, rolling back to
the old snapshot loses those new writes; take another backup and plan their
reconciliation before reverting.

Renaming a container does **not** remove its Compose labels. Compose can
recreate a renamed rollback container belonging to the same project. Use a
distinct project for the migration drill and retain a reproducible rollback
definition or stopped containers without the active project's Compose labels.
