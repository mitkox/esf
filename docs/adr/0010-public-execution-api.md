# ADR 0010 — Public execution API

- **Status:** Accepted
- **Date:** 2026-11-07
- **Deciders:** ESF maintainers
- **Supersedes:** the sentence in `ARCHITECTURE.md` stating that the factory
  exposes no general-purpose control-plane HTTP API. That statement described
  the MVP; it no longer describes the product.

## Context

The factory's CLI is the control plane and the security boundary. It accepts
only an approved repository, an exact revision and task text. Integrators
building on the factory, and a work-item pipeline that must submit work without
shelling out to a binary, need a durable programmatic submission surface.

Two existing surfaces are not that surface:

- The console is deliberately read-only and documented as not a second control
  plane. Extending it would give one process read and write authority whose
  separation is currently a security property.
- The sf turn executor serves a private execution protocol to the factory host.
  Its callers supply profiles, manifests and an owner token, and it explicitly
  disclaims scheduling workflows, publication and the factory's input policy.

The API must not become a fourth input bucket. The caller may select only an
approved repository, an exact revision and task text, plus names of resources
the operator already declared. It may not supply an executable, an argument
vector, a sandbox template id, a host path, an environment variable, a
credential or a verification command.

## Decision

Add a versioned execution API as a new package and listener.

### Listener and transport

- `internal/api` owns the wire contract and the HTTP listener. It reuses
  `internal/httpsecurity` for listener validation, bearer authentication and TLS
  material loading, and `internal/secretfile` for the operator token file.
- The token file is read once at startup through the private-file reader: a
  regular file, no symlinks, owner-only permissions, bounded size. The token
  is never logged and never returned in a response.
- An absent or unreadable token file fails startup. A group- or
  other-readable token file fails with the private-file error.
- A non-loopback address requires native TLS. The rule is applied to the
  requested address and again to the address that was actually bound.
- Submission bodies are capped at 1 MiB and decoded strictly: one JSON value,
  unknown fields rejected. Requests carry per-request bearer authentication;
  exactly one `Authorization: Bearer` header is accepted, and the comparison is
  constant time. Server read header, read, write and idle timeouts are bounded,
  and shutdown drains on context cancellation.
- The operator enables the listener with an `[api]` configuration section:
  `enabled`, `listen`, `token_file`, `tls_cert_file` and `tls_key_file`. The
  section is decoded strictly and validated with the configuration's accumulated
  problem list so every mistake is reported at once. The API is disabled by
  default.

### Contract

The API is versioned under `/v1` and is additive-only within that major
version:

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/v1/runs` | Submit a run request |
| `GET` | `/v1/runs/{run_id}` | Read run status |
| `GET` | `/v1/runs/{run_id}/events?after=N` | Read sequenced step conditions |
| `GET` | `/v1/runs/{run_id}/evidence` | Read the durable run manifest |
| `POST` | `/v1/runs/{run_id}/cancel` | Request cancellation |

`POST /v1/runs` carries a run ID, an approved repository, an exact revision,
task text, and the names of operator-declared harness, verification profile,
scope, workspace, model and egress resources. The command fills the sandbox
template and timeouts from operator configuration; the caller cannot set them.
Omitted harness and verification profile names resolve to the operator's
configured defaults, exactly as the CLI does.

Errors carry a stable machine-readable `code` (`invalid_request`,
`run_not_found`, `request_too_large`, `unauthorized`, `internal`) plus a
human-readable message. An unknown run is a typed not-found response, never an
empty success.

The evidence endpoint returns the run's durable `run.json` manifest, which the
artifact model already defines as the authoritative record of a finished run.
It is not a substitute for the signed evidence bundle; that bundle is verified
offline with `factory verify`.

### Pipeline boundary (2026-11-08)

The API remains the external submission surface. The first-party work-item
pipeline is not an API client: it is the reference in-process consumer of the
same submission contract. Both entry points pass their values through one
shared preparation step that resolves unset operator defaults, normalizes the
request and applies the shared validation, so the pipeline keeps its
scope-aware harness and verification defaults and adds only its own work-item,
forge-connector and review fields. A conformance test in `internal/factory`
proves the two paths produce equal shared request fields and equal validation
errors.

### Idempotent submission

The workflow ID is derived from the run ID with the factory's existing
deterministic mapping. Submission reuses a running workflow with the same
identity and refuses to start a second execution for a closed one. A repeated
submission therefore starts one workflow; the response acknowledges the
activation and clients poll status for execution state.

### Security boundary

The API adds no capability that the operator did not already register. It
cannot select a local host path, including a registered one: only the CLI's
trusted local-fixture path remains available for host-directory fixtures. It
cannot pass provider credentials, Cube management credentials or a shell
command. It never merges, approves or publishes anything; those remain separate
contracts.

## Consequences

- A new authenticated network surface exists. It is opt-in, loopback by default
  in documentation, requires TLS off loopback, and its token is a separate
  operator-managed credential.
- The console stays read-only and the turn executor stays private; neither
  gains write authority.
- Integration is versioned at `/v1`. Adding response fields or endpoints is
  compatible; removing or renaming one requires a new major version and a
  compatibility note.
- Identity is still operator-level: the API distinguishes its own bearer
  credential from other factory credentials, but it does not introduce
  per-user accounts or forge identity. That remains a separate decision.
- Run status falls back to the durable manifest when Temporal history is no
  longer queryable, so a completed run stays inspectable after history
  retention.
- A caller that submits work still depends on the operator's Cube, Temporal and
  policy configuration being valid; the API does not create execution
  capability on its own.
