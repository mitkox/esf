# sf turn execution service

`cmd/turn-executor` is a private, single-process execution service over an existing
Cube deployment. sf remains the workflow, workspace, connector, permission and
acceptance authority. This service creates one fresh VM per turn, restores a
content-addressed binary manifest, runs a named operator profile, checkpoints
files and Claude session state, and verifies destruction. It does not run a
second Temporal workflow or publish Git branches.

## Run

Build `go build -o bin/turn-executor ./cmd/turn-executor`. Configure the existing
`CUBE_*` provider variables and a random `ESF_TURN_SERVICE_TOKEN` of at least 32
characters. Start with `bin/turn-executor --listen 127.0.0.1:8091 --state
/var/lib/esf/turns --profiles /etc/esf/turn-profiles.json`. Run one instance per
state directory; an exclusive lock prevents concurrent owners. Put TLS and
private network access in front of non-loopback deployments. Never expose this
service directly as a browser API. sf uses `SF_ESF_URL` and `SF_ESF_TOKEN`.

The template needs Python 3 and the provider's existing output-limiter tools.
Claude can be preinstalled or staged from an operator-owned file with a pinned
SHA-256; client requests cannot select a file, command, template or credential.
The pinned native Claude 2.1.272 Linux x64 binary used in local qualification has
SHA-256 `d81396a668eb76fbddb49a2a5841f1b5d7af96b4c1f6500ced92f2c988f5bcd4`.

Example operator profile (replace the template and secret *reference*, not with
secret values in this file):

```json
{
  "regatta-chat": {
    "template": "READY_TEMPLATE_ID",
    "command": ["/tmp/esf-harness", "-p", "--verbose", "--output-format", "stream-json", "--permission-mode", "default", "--setting-sources", "", "--model", "minimax-m3"],
    "harness": "claude-code",
    "harness_artifact": {"file": "/opt/esf/claude/2.1.272/claude", "sha256": "d81396a668eb76fbddb49a2a5841f1b5d7af96b4c1f6500ced92f2c988f5bcd4"},
    "model_base_url": "https://opencode.ai/inference/go",
    "env_refs": {"ANTHROPIC_API_KEY": "OPENCODE_GO_API_KEY"},
    "network": {"allow_internet": true},
    "timeout_seconds": 1800,
    "max_cost_usd": 1,
    "interactive_permissions": true,
    "mcp_servers": {"sf_connectors": {"url": "https://connectors.internal/mcp", "bearer_env_ref": "SF_REGATTA_CONNECTOR_TOKEN"}}
  }
}
```

The MCP credential must be scoped to its configured workspace. Do not inject raw
GitLab/Notion/Slack credentials into the agent environment. This service does not
turn a broad connector token into scoped authorization. Profiles and the Cube
network/template are trusted operator policy. Remote client input is not policy.

## Private protocol

Every endpoint requires `Authorization: Bearer <service token>` and returns JSON.

- `POST /v1/blobs`: `{files:[{path,content_base64}],harness_session_id?}` returns
  `{sha256}`. Relative paths only; no symlinks/special files, overlaps or duplicates.
- `GET /v1/blobs/:sha`: validates the stored digest before returning the manifest.
- `POST /v1/executions`: `{execution_id,profile,spec_sha,prompt,owner_token,
  manifest?,checkpoint?,session_id?,mode?,profile_sha?,policy_sha?,metadata?,max_cost_usd?}`.
  A repeated identical ID/request/owner returns the same execution. A conflict is
  409. Persisted records contain an ownership hash, never the raw ownership token.
- `GET /v1/executions/:id`: status, lease expiry and pending permission operations.
- `GET /v1/executions/:id/events?after=N`: monotonically numbered lifecycle,
  stdout, stderr, permission and decision events, replayable after reconnect.
- `GET /v1/executions/:id/result`: sf-compatible `TurnResult`, checkpoint reference
  and backend error. While active this returns 409.
- `POST /v1/executions/:id/renew` and `/cancel`: `{owner_token}`. Lease duration is
  60 seconds; clients renew at least every 20 seconds. Expired leases cannot revive.
- `POST /v1/executions/:id/permissions`: `{owner_token,permission_id,
  operation_digest,decision:"allow"|"deny"}`. Different or stale answers fail closed.
  Identical duplicate answers are idempotent. Approval is durable before delivery.
- `GET /v1/profiles/:name`: immutable `profile_sha` configuration receipt, optional
  compiled workflow policy digest and names of
  model credential references; no credential values or operator commands.

Clients freeze `profile_sha` before reserving an execution; changed profiles are
rejected before model execution. Profile references contain no secret values.
Deployment unit and secret-free sample: `deploy/turn-executor/`.

Claude's `--permission-prompt-tool` invokes a staged stdio MCP gate. A bounded
operation file is polled by the service and surfaced to sf; the exact answer is
returned to the gate by ID and operation digest. No shell interpolation is used.
The bridge does not persist blanket allow rules. Selection profiles have no
permission bridge, MCP servers or built-in tools.

## Workflow and selector boundaries

For selectors use `selection_only:true`, `harness:"claude-code"`, and a one-element
command containing the approved executable. The request must have
`mode:"selection"`. The service generates restricted/no-tools arguments itself,
rejects manifests and checkpoints, and limits the selector to one model turn.

For compiled workflow turns set `turn_policy` to the exact projection of the sf
TurnSpec's `harness`, `tools`, `sandbox` (network, timeout, memory, CPUs), and optional
`output`. Only Claude, a single approved executable, explicit permission modes,
explicit integer `m`/`g` memory and numeric CPU resources, and matching profile
network policy are accepted. The
service generates argv from that policy and requires its digest on every request.
Because Cube allocation is template-driven, the provider checks the actual VM
CPU/memory receipt before staging or invoking the harness; mismatches fail closed.
Such profiles cannot add MCP resources, interactive permissions or non-model
credentials. Only `SF_STEP`, `SF_PRODUCES`, `SF_INPUTS`, `SF_ANSWERS` are accepted as
request metadata. The sf adapter separately checks the exact model endpoint and
credential-name set.

sf enables these turns only with `SF_TURN_ESF_PROFILE` (or explicit RunOptions).
The adapter currently accepts local materialized sources, verifies immutable
inputs/bundle before output copyback, and rejects Git publication, source symlinks,
file deletion and backend overrides. This is a persisted-input integrity check,
not a claim that the VM presents read-only filesystem mounts. Interrupted remote
workflow attempts stop for reconciliation instead of launching duplicate work.
The interactive workflow-to-asks bridge and full Git workflow publication remain
separate work; chat uses the interactive permission bridge today.

## Durability, bounds and cost evidence

Execution records and blobs use atomic writes and filesystem synchronization.
After restart, active turns are failed and their sandbox is destroyed, including
the create-response-loss window recovered through provider metadata. They are
never silently re-executed. Terminal results require checkpoint persistence and
verified cleanup. Storage must be durable and backed up by the operator; local
files are not replicated storage or a demonstrated Azure recovery objective.

Limits: 16 concurrent turns, 16 MiB encoded manifests, 4096 regular files per
manifest, 2 MiB combined stdout/stderr, 16 KiB per permission operation and 1000
permission decisions. The stdout/stderr wrapper kills a child that exceeds its
output bound. A profile timeout caps each execution; cancellation always attempts
cleanup with a separate context. Blob/record retention is currently operator
managed; the service does not promise automatic TTL collection.

The service passes Claude `--max-budget-usd` using the lower request/profile cap
(profile default 1 USD, maximum 100 USD). This is the harness's accounting guard,
not a provider billing reservation. Missing usage remains unknown. In particular,
Claude emits `unrecognized_model` for MiniMax M3 and reports fallback pricing:
that number is retained as `backend.harness_reported_cost_usd` but is **not**
reported as covered `usage.cost_usd`; `cost_status` is `unknown_model_pricing`.
A workflow requiring covered spend must stop until pricing evidence is available.

## Retained local validation, 14 October 2026

- Go protocol/lifecycle suites ran inside disposable Cube VMs, including binary
  checkpoints, idempotency conflicts, auth/path rejection, restart without replay,
  ownership expiry/cancel, stale permission binding and the Python MCP protocol.
- sf HTTP client drove two real Cube fixture turns with a binary checkpoint and
  fresh VM restoration. Each sandbox ID was absent after teardown.
- A live fixture MCP approval rejected the wrong operation digest, accepted the
  exact approval, resumed the child and verified cleanup. A real Claude turn also
  paused through the same MCP permission bridge, received one bound approval,
  wrote the requested checkpoint file and completed with verified cleanup.
- Pinned Claude 2.1.272 ran through OpenCode Go MiniMax M3 and returned a sentinel;
  a second fresh VM resumed its saved conversation and recalled that sentinel.
  The credential was absent from checkpoint content. This is a model-connectivity
  and conversation-restoration proof, not an exact provider-cost proof.

- A real compiled `runTurn` used the policy receipt, wrote an artifact through
  Claude, restored its checkpoint, verified the immutable inputs and copied the
  output back with a recomputed digest and verified cleanup. The local template
  reports 2 CPUs/2000MiB: a 4 GiB policy was rejected before model execution, then
  the exact 2000m policy passed. Missing or fractional CPU receipts are covered
  by negative provider regression cases.

Official CLI flags: https://code.claude.com/docs/en/cli-reference.
OpenCode Go supported clients/endpoints: https://opencode.ai/v2/docs/console/go/.

### Rebase validation

The original model receipts above are tagged `fbd3267`. After rebasing onto
`0ef8e23`, both affected Go packages passed inside Cube and the rebuilt lifecycle
probe passed with `FACTORY_CUBE_VERSION=0.7.2`, including binary roundtrip, metadata
isolation and verified owned-VM cleanup. No model calls were repeated. Retained
logs and new binary digests are in `docs/evidence/sf-turn-rebase-20261014.json`.

### Merge preparation validation, 15 October 2026

The branch now includes master `3cf6348`. Compiled policies explicitly disable
implicit MCP servers from restored files. Claude completion requires a terminal
result event, and completion after ownership expiry cannot publish a checkpoint.
All three regression tests fail against the pre-fix code and pass with the fixes.
The 14 executor and 13 provider test cases passed with the race detector inside
Cube. Binary roundtrip and metadata isolation passed with internet enabled and
disabled; the conformance factory run passed verification and cleanup. The final
leak check found no live sandboxes. This validation used the local Cube stack;
no model calls were repeated. Earlier model receipts remain revision-tagged.
The factory conformance metadata probe was inconclusive; the separate lifecycle
probes passed. Azure, production qualification and sf acceptance remain separate
validation gates.

## Transport and credential configuration

Plain HTTP is restricted to explicit loopback listeners. A remote or wildcard
`--listen` requires `--tls-cert-file` and `--tls-key-file`; startup refuses
remote plaintext before connecting to Cube. Native TLS requires a currently
valid certificate and a private regular key file, and negotiates TLS 1.2 or newer.
A TLS reverse proxy can instead forward to a loopback backend.

Prefer `--token-file /path/to/service.token` for a private regular file holding
at least 32 non-whitespace characters. It overrides `ESF_TURN_SERVICE_TOKEN`.
Create tokens with a restrictive umask and keep them mode 0600. Symlinks,
group/other permissions, and token files larger than 4096 bytes are refused.
Every API request still requires one `Authorization: Bearer <token>` header;
query credentials and duplicate Authorization headers are rejected.

These credentials identify one trusted service/operator domain. They do not
provide per-user roles or tenant isolation. Restrict backend access and place
organizational identity/access controls at the proxy for shared deployments.

Bearer tokens use ASCII letters/digits and `-._~+/`, optionally followed by `=`
padding. Generate a 32-byte random hex token (`openssl rand -hex 32`). Startup
rejects commas, whitespace and other characters that cannot be used as a bearer
credential; replace such legacy credentials before restarting.
