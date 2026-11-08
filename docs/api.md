# Execution API

The execution API is the versioned HTTP surface for submitting factory work and
reading what happened to it. It is separate from the read-only console and from
the private turn executor: it is the only network surface that can submit a run.

The caller supplies the same three inputs as the CLI — an approved repository,
an exact revision and task text — plus the names of resources the operator has
already declared. It cannot supply an executable, an argument vector, a sandbox
template id, a host path, an environment variable, a credential or a
verification command.

## Serve the API

Generate a bearer token, keep it in a private file, and enable the listener in
`factory.toml`:

```sh
mkdir -p /etc/factory
openssl rand -hex 32 > /etc/factory/api.token
chmod 600 /etc/factory/api.token
```

```toml
[api]
enabled = true
listen = "127.0.0.1:8090"
token_file = "/etc/factory/api.token"
```

```sh
factory api --config /etc/factory/factory.toml
```

- The token file is read at startup through the private-file rules: a regular
  file, no symlinks, owner-only permissions, bounded size. A missing or
  group-readable file fails startup.
- A loopback listener may serve HTTP behind a TLS-terminating proxy. Any
  non-loopback listener requires native TLS via `tls_cert_file` and
  `tls_key_file`, set together.
- The listener is disabled by default. Enabling it adds an authenticated
  network surface but no capability the operator did not already register.

## Versioning and compatibility

All routes live under `/v1`. A major version is additive-only:

- Adding a response field, an event type or an endpoint is compatible.
- Removing or renaming a field, changing its JSON type, or changing the meaning
  of an existing value requires a new major version and a compatibility note.
- Clients must ignore response fields they do not know.

The checked-in fixture `internal/api/testdata/contract-v1.json` is the wire
shape of the current major version. The contract tests exercise each response
and require every fixture field to remain present with the same JSON type; they
fail if a field is removed or renamed. A contract test also submits the fixture
request, so the request shape is held to the same rule.

## Authentication

Every request carries one header:

```
Authorization: Bearer <token>
```

Exactly one `Authorization` header is accepted, and the token is compared in
constant time. A missing or wrong token returns `401` with `WWW-Authenticate:
Bearer realm="ESF execution API"`. Tokens are never accepted in a query string.

## Endpoints

| Method | Path | Purpose | Success |
| --- | --- | --- | --- |
| `POST` | `/v1/runs` | Submit a run request | `202` with run status |
| `GET` | `/v1/runs/{run_id}` | Read run status | `200` with run status |
| `GET` | `/v1/runs/{run_id}/events?after=N` | Read sequenced step conditions | `200` with an event array |
| `GET` | `/v1/runs/{run_id}/evidence` | Read the durable run manifest | `200` with evidence |
| `POST` | `/v1/runs/{run_id}/cancel` | Request cancellation | `202` with run status |

Identifiers are encoded as single URL path segments. Request and response
bodies are JSON; responses carry `Content-Type: application/json; charset=utf-8`.

## Submit a run

`POST /v1/runs` accepts exactly one JSON object. Unknown fields are rejected
rather than ignored, and the body is capped at 1 MiB.

```json
{
  "run_id": "run-20261107-1",
  "repository": "https://github.com/acme/repository",
  "revision": "0123456789abcdef0123456789abcdef01234567",
  "task": "Fix the failing test and explain the root cause.",
  "agent_harness": "opencode",
  "verification_profile": "default",
  "scope": "team-a"
}
```

| Field | Required | Meaning |
| --- | --- | --- |
| `run_id` | yes | Caller-chosen stable identity. The workflow ID is derived from it. |
| `repository` | yes | A repository the operator has approved. |
| `revision` | yes | The exact base revision to run against. |
| `task` | yes | The task text shown to the agent. |
| `agent_harness` | no | Name of an operator-declared harness; empty uses the configured default. |
| `agent_model` | no | Name of an operator-declared model for the harness. |
| `verification_profile` | no | Name of an operator-declared profile; empty uses the configured default. |
| `scope` | no | Name of an operator-declared scope. |
| `workspace` | no | Name of an operator-declared workspace. |
| `model` | no | Name of an operator-declared model resource. |
| `egress_policy` | no | Name of an operator-declared egress policy. |
| `change_id` | no | Durable work-item identity this run belongs to. |
| `parent_run_id` | no | Parent run for a retry or follow-up attempt. |

Submission is idempotent for one `run_id`: a repeated submission reuses the
running workflow and a closed workflow is not restarted. Reusing a run identity
with a different repository, revision, or task returns `409` with code
`run_conflict`; resubmitting a closed run returns `409` with code `run_terminal`.
The `202` response acknowledges the activation and reports the existing run's
state; poll status for execution state.

The response is the existing run's status when it is already queryable; a newly
started run that is not yet queryable is reported as `REQUESTED`.

## Run status

`GET /v1/runs/{run_id}` returns:

| Field | Meaning |
| --- | --- |
| `run_id` | The run identity. |
| `workflow_id` | The durable workflow identity. |
| `state` | One of `REQUESTED`, `RUNNING`, `PAUSED`, `AGENT_FAILED`, `VERIFICATION_FAILED`, `EVIDENCE_FAILED`, `SUCCEEDED`, `CANCELLED`, `INFRASTRUCTURE_FAILED`, `INVALID_REQUEST`, `BLOCKED`, `QUARANTINED`, `GATE_TAMPERED`, `EGRESS_UNVERIFIED`, `REJECTED`. |
| `current_step` | The step currently executing or last recorded. |
| `repository`, `revision` | The repository and revision the run was submitted for. |
| `scope`, `change_id` | The resolved tenancy scope and work-item lineage, when present. |
| `agent_harness`, `verification_profile` | The resolved operator resources. |
| `agent_result`, `verification_result` | Separate agent and deterministic-gate outcomes. A successful agent result never implies a successful verification. |
| `started_at`, `completed_at` | RFC 3339 timestamps. `completed_at` is the zero timestamp until the run finishes. |
| `conditions` | Step transitions; omitted when no condition has been recorded. |
| `error` | The recorded failure text, when present. |

Fields that are empty may be omitted. A client must not treat an omitted field as
a removal from the contract.

A condition has:

| Field | Meaning |
| --- | --- |
| `type` | The step or condition kind. |
| `status` | `True`, `False` or `Unknown`. |
| `reason` | Short machine-readable cause, when present. |
| `message` | Human-readable detail, when present. |
| `last_transition_time` | RFC 3339 timestamp of the transition. |

An unknown run is a `404` with `code: "run_not_found"`, never an empty success.

## Events

`GET /v1/runs/{run_id}/events?after=N` returns events with `seq > N`:

```json
[
  {
    "seq": 1,
    "type": "Validated",
    "status": "True",
    "reason": "ChecksPassed",
    "message": "all checks passed",
    "time": "2026-11-07T10:02:00Z"
  }
]
```

`after` is a non-negative integer and defaults to `0`. Events are derived from
the run's conditions, so a cursor is stable across polls: store the last `seq`
you processed and pass it as `after` on the next request.

## Evidence

`GET /v1/runs/{run_id}/evidence` returns:

```json
{
  "run_id": "run-20261107-1",
  "manifest": {"run_id": "run-20261107-1"}
}
```

`manifest` is the run's durable `run.json`, returned byte for byte as a JSON
object. It is the authoritative record of a finished run and stays readable after
Temporal history retention. It is not the signed change-evidence bundle; that
bundle is verified offline with `factory verify` as described in
[evidence-contract.md](evidence-contract.md).

## Cancel

`POST /v1/runs/{run_id}/cancel` has no request body. It requests cancellation
and returns the run's current status with `202`. Cancellation of an unknown run
is a `404` with `code: "run_not_found"`. Cancellation does not merge, approve or
publish anything; it only asks the workflow to stop.

## Errors

Error bodies are one JSON object:

```json
{"error": "The run does not exist.", "code": "run_not_found"}
```

| Status | `code` | Meaning |
| --- | --- | --- |
| `400` | `invalid_request` | The body, field or cursor cannot be honoured. |
| `401` | `unauthorized` | The bearer token is missing, malformed or wrong. |
| `404` | `run_not_found` | The named run does not exist. |
| `404` | `not_found` | The path is not an API endpoint. |
| `409` | `run_conflict` | The run identity is executing a different request. |
| `409` | `run_terminal` | The run identity is closed and cannot be restarted. |
| `413` | `request_too_large` | The submission body exceeds 1 MiB. |
| `500` | `internal` | The API could not complete the request. |

The `code` is stable and machine-readable; `error` is a human-readable message
that may change.

## Go client

Code inside this module uses `internal/apiclient` instead of the server package.
It carries the API's wire types and turns non-2xx responses into typed errors:

```go
import (
	"context"

	"github.com/mitkox/esf/internal/apiclient"
)

client, err := apiclient.New("https://factory.example.internal:8443", token)
if err != nil {
	return err
}

status, err := client.Submit(ctx, apiclient.RunRequest{
	RunID:               "run-20261107-1",
	Repository:          "https://github.com/acme/repository",
	Revision:            "0123456789abcdef0123456789abcdef01234567",
	Task:                "Fix the failing test.",
	AgentHarness:        "opencode",
	VerificationProfile: "default",
})
if err != nil {
	return err
}

events, err := client.Events(ctx, status.RunID, 0)
if err != nil {
	return err
}
_ = events

evidence, err := client.Evidence(ctx, status.RunID)
if err != nil {
	return err
}
_ = evidence

status, err = client.Cancel(ctx, status.RunID)
```

Methods:

| Method | API operation |
| --- | --- |
| `Submit(ctx, apiclient.RunRequest)` | `POST /v1/runs` |
| `Status(ctx, runID)` | `GET /v1/runs/{run_id}` |
| `Events(ctx, runID, after)` | `GET /v1/runs/{run_id}/events` |
| `Evidence(ctx, runID)` | `GET /v1/runs/{run_id}/evidence` |
| `Cancel(ctx, runID)` | `POST /v1/runs/{run_id}/cancel` |

`New` requires an `http` or `https` base URL and a non-empty token. It applies a
60 second request timeout. Error classes:

- `*apiclient.APIError` for a non-2xx response; it carries `StatusCode`, `Code`
  and `Message`.
- `errors.Is(err, apiclient.ErrRunNotFound)` for the `run_not_found` class.
- `*apiclient.ResponseError` for a successful response that is not valid JSON or
  omits a field the response contract requires.
- Any other error is a transport or request-encoding failure.

## Security boundary

The API never merges, approves or publishes anything. Those remain separate
contracts. It adds no capability the operator did not register: the caller
selects an approved repository, an exact revision, task text and resource names,
and cannot widen the run beyond them. The API distinguishes its bearer
credential from other factory credentials but does not introduce per-user
accounts or forge identity.
