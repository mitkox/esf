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

## What runs

| Service | Image | Host binding | Purpose |
| --- | --- | --- | --- |
| `postgresql` | `postgres:16.15-alpine3.24` | `127.0.0.1:5433` | Temporal persistence + visibility store |
| `temporal-admin-tools` | `temporalio/admin-tools:1.32.0` | — | one-shot schema setup |
| `temporal` | `temporalio/server:1.32.0` | `127.0.0.1:7233` | gRPC frontend |
| `temporal-create-namespace` | `temporalio/admin-tools:1.32.0` | — | one-shot namespace creation |
| `temporal-ui` | `temporalio/ui:2.55.0` | `127.0.0.1:8233` | web UI |

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

Temporal's own state lives in the `factory-temporal-postgresql-data` Docker
volume, so workflow history survives `make temporal-down`. Because
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
