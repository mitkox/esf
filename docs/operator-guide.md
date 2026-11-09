# Operator Guide

Everything an operator needs to run, inspect, and extend the factory.

Before deployment, review [production readiness](production-readiness.md) for
validated behavior, deployment boundaries, and live acceptance checks.

The current candidate is ESF 1.0.0-rc.1, a release candidate with outstanding GA gates; see
the [1.0.0-rc.1 release notes](release-v1.0.0-rc.1.md) for release status,
the Harness2 improvement PoC removal and the operator migration steps.

---

## 1. What the factory discovers and configures

The factory **consumes** an existing CubeSandbox installation. It never installs,
reconfigures, resets, or removes Cube components, and it never creates, mutates,
or deletes templates.

### How Cube is discovered

| Setting | How to find it | This host |
| --- | --- | --- |
| API endpoint | `GET /health` on the Cube API port | `http://127.0.0.1:4000` |
| Template | `cubemastercli tpl list`, pick a `READY` row | `tpl-your-ready-template` |
| Proxy node | data-plane host for `Host: <port>-<id>.cube.app` | `192.0.2.10` |
| Proxy port | `ss -tlnp` on the proxy node | `80` |
| Version | `/usr/local/services/cubetoolbox/VERSION.txt` | `v0.7.3-amd64` |

```bash
# Confirm the endpoint first: the SDK's default is port 3000, which is NOT this
# deployment's port.
curl -s http://127.0.0.1:4000/health
cubemastercli tpl list
./bin/factory doctor
```

`factory doctor` validates configuration, proves the Cube API answers, proves the
configured template exists, confirms Temporal is reachable, and lists the
registered harnesses. Run it before anything else.

### Which template is used

The value in `cube.template_id`, overridable per run with `--sandbox-template`
(an operator-facing flag, not an API-client capability). The factory never
creates a template. If none suits, the production direction is a new, versioned
factory template (`factory-agent-base-v1`) — a deliberate deployment change, made
by the operator.

## 2. Starting Temporal

```bash
make temporal-up       # start and wait for health
make temporal-status   # containers + namespace
make temporal-down     # stop (the data volume is preserved)
make temporal-clean    # stop AND delete the data volume
```

| Endpoint | Value |
| --- | --- |
| gRPC | `127.0.0.1:7233` |
| UI | <http://127.0.0.1:8233> |
| Namespace | `default` |
| PostgreSQL | `127.0.0.1:5433` |

All ports bind to `127.0.0.1`. Temporal is never reachable off-host.

```bash
make temporal-hello    # proves gRPC + namespace + a workflow actually executing
```

## 3. Where factory state lives

| State | Location | Notes |
| --- | --- | --- |
| Workflow state | Temporal (Docker volume `factory-temporal-postgresql-data`) | Survives `make temporal-down` |
| **Artifacts / evidence** | `.factory/runs/<run-id>/` (configurable via `storage.data_dir`) | Survives sandbox destruction |
| Configuration | `factory.toml` (or `$FACTORY_CONFIG`) | Gitignored locally; `factory init` writes a template |
| Worker log | stdout, or `.factory/worker.log` when started by the demo script | |

## 4. Running a task

```bash
./bin/factory run \
  --repo https://github.com/your-org/your-repo \   # or --local-path /path/to/repo
  --rev <exact-revision> \
  --task "Change the greeting from hello to hello factory" \
  --agent opencode2 \
  --verification default
```

The CLI prints the run id, workflow id, harness, template, and — on completion —
the three outcomes plus the patch path:

```
FACTORY RESULT: SUCCEEDED
AGENT RESULT:   SUCCESS
VERIFICATION:   PASSED
CLEANUP:        PASSED

Patch:
.factory/runs/run-…/changes.patch
```

Useful flags:

| Flag | Purpose |
| --- | --- |
| `--local-path` | Use a repository that exists on the factory host (packed as a git bundle). Used by the acceptance fixture. |
| `--model` | Override the harness's default model. |
| `--sandbox-template` | Override the template for this run. |
| `--run-id` | Supply an explicit run id (makes submission idempotent). |
| `--wait=false` | Submit and return immediately. |
| `--agent-timeout` | Override the agent timeout. |
| `--scope` | Run under a tenancy scope; the scope's declared resources apply. |
| `--workspace` | Use a named pre-warmed workspace. |
| `--egress` | Run under a named network policy. |
| `--change` | Attach the run to a durable change (default: the run itself). |
| `--parent-run` | Record the run this one reworks. |
| `--review` | Pause for human review before finalizing. |

### Declarative resources and the review gate

A run may reference operator-declared resources instead of repeating settings:
`[workspaces]`, `[models]`, `[egress]`, `[budgets]` and `[scopes]` in
`factory.toml`. Resolution happens once, in validation, and the resolved set
plus its digest is recorded in `run.json`. Unknown names are rejected — a typo
fails loudly rather than running with no policy.

With `[review] enabled = true` (or `--review`), a run pauses after the gates
report:

```bash
./bin/factory status <run-id> --watch          # stream conditions until it pauses
./bin/factory review <run-id> --approve
./bin/factory review <run-id> --reject --note "use the formal greeting"
```

The sandbox is checkpointed while paused when the provider supports it (the
manifest records `suspend_result`), preview URLs are published when
`preview_ports` is set (`preview_result` records `SUCCESS`, `FAILED`, or
`SKIPPED`), and a timeout is recorded as `timeout` — the run then finishes
normally. A rejection does not rewrite the gate result: the factory result
stays `SUCCEEDED` if the gates passed, and the redacted review note becomes the
change's rework instruction while the change returns to `OPEN`.

### Changes (durable work items)

Every run belongs to a change. Without `--change`, the run is its own change.
With `--change`, rework activations accumulate lineage and spend:

```bash
./bin/factory get changes
./bin/factory describe change <change-id>
./bin/factory run --change <id> --parent-run <reviewed-run-id> ...   # rework
```

### Operator access to a live sandbox

`factory attach` and `factory preview` are **default closed**. Enable them with
`[sandbox] allow_attach = true` / `allow_preview = true`; every use is audited
in `audit/actions.jsonl`. Neither resumes a suspended sandbox implicitly.

```bash
./bin/factory attach <run-id> -- ls -la /workspace/repository
./bin/factory preview <run-id> --port 3000
```

### Result states

| State | Meaning |
| --- | --- |
| `SUCCEEDED` | Every mandatory gate passed, patch collection completed, and sandbox cleanup was verified |
| `AGENT_FAILED` | The agent exited non-zero or timed out |
| `VERIFICATION_FAILED` | The agent claimed success; a deterministic gate disagreed |
| `INFRASTRUCTURE_FAILED` | Infrastructure, patch collection, or cleanup failed |
| `INVALID_REQUEST` | Rejected by policy; no sandbox was created |
| `PAUSED` | Waiting on a human review gate; the run resumes or times out |
| `CANCELLED` | The workflow was cancelled |

## 5. Inspecting a run

```bash
./bin/factory status <run-id>          # manifest if finished, live query otherwise
./bin/factory status <run-id> --json
./bin/factory status <run-id> --watch  # stream condition transitions
./bin/factory describe run <run-id>    # manifest + conditions + inventory + audit
./bin/factory logs   <run-id>          # artifact listing + agent output tails
```

Artifact layout:

```
.factory/runs/<run-id>/
  task.json                 the request as accepted (+ task_hash)
  baseline.json             source, requested revision, resolved SHA, branch, status
  git-before.txt            what the agent started from
  changes.patch             THE DELIVERABLE
  git-after.txt             post-run sha and status
  run.json                  the manifest
  cleanup.json              destroy outcome, verification, remaining sandboxes
  agent/{prompt.txt,stdout.log,stderr.log,result.json}
  hardening/egress-probe.json    what the sandbox could actually reach
  hardening/behavior.json        the behavior scan of the agent's output
  hardening/patch-scan.json      the behavior scan of the collected patch
  hardening/gate-integrity.json  whether the tree modified the gates it runs
  verification/{result.json,<gate>.stdout,<gate>.stderr}
  evidence/bundle.json      signed change-evidence bundle
  evidence/signature.json   detached signature sidecar
```

Quick triage:

```bash
R=.factory/runs/<run-id>
jq '{factory_result,agent_result,verification_result,cleanup_result}' $R/run.json
jq '.steps[] | {id,exit_code,passed,error}' $R/verification/result.json
jq '.hardening' $R/run.json          # boundary, behavior, gates, alerts
cat $R/changes.patch
```

`factory status` prints the hardening record too, including an explicit
`not measured` / `not scanned` when a control is disabled, so a clean run is
distinguishable from an unmonitored one.

## 5b. Factory console (read-only dashboard)

`factory console` serves an embedded, read-only dashboard over the durable
evidence: overview, dispatch command composer, runs and patches, changes,
review queue, security, operations and redacted settings. It never submits a
run, approves a review or cancels a workflow.

```sh
make console-build
openssl rand -hex 32 > ~/.config/esf/console.token && chmod 600 ~/.config/esf/console.token
./bin/factory console --read-token-file ~/.config/esf/console.token \
  --tls-cert-file ~/.config/esf/tls/console.crt \
  --tls-key-file ~/.config/esf/tls/console.key
```

The listener is loopback-only by default; non-loopback binds require the read
token. Native TLS enforces a TLS 1.2 floor and HSTS, refuses a world-readable
key, and throttles repeated failed bearer attempts. A reverse proxy (nginx or
Tailscale serve/funnel) works too. See the [console guide](console.md).

## 6. Cancelling a run

```bash
./bin/factory cancel <run-id>          # stop one run
./bin/factory halt --harness unreal    # dry run: list what would stop
./bin/factory halt --harness unreal --yes
./bin/factory halt --scope payments --yes
```

`halt` filters on the harness, scope and repository recorded in the run's
workflow memo, and is a **dry run** unless `--yes` is passed: stopping many runs
at once is a containment action with real cost.

Cancellation is durable: the workflow's cleanup runs from a **disconnected
context**, so the microVM is destroyed even though the workflow was cancelled.
This is covered by an integration test.

If the product command is unavailable, the workflow can still be cancelled
directly:

```bash
temporal workflow cancel --address 127.0.0.1:7233 \
  --namespace default --workflow-id factory-run-<run-id>
```

## 6a. Hardening: boundary, behavior, and containment

The factory measures the sandbox boundary instead of assuming it, scans agent
output for out-of-bounds behavior, and stops a run that crosses either line. See
[ADR 0007](adr/0007-defense-in-depth.md) for why each control exists.

```toml
[hardening]
# The factory refuses to start unless every egress policy denies public
# internet, or you acknowledge the risk here. Preparation runs inside the
# sandbox, so a setup phase usually needs a network path.
acknowledge_open_egress = true

# Measure, per run, what the sandbox can actually reach. Absent means enabled.
egress_probe = true
egress_canary_url = "https://example.com/"
metadata_probe_url = "http://169.254.169.254/latest/meta-data/"
require_non_root = false

# Scan agent output and quarantine a run that trips the threshold.
behavior_monitor = true
trip_severity = "HIGH"
# trip_rules_file = "/etc/factory/trip-rules.json"

allow_gate_self_modification = false

# alert_webhook_url = "https://hooks.example.org/esf"
# alert_webhook_token_file = "/run/credentials/factory-worker.service/alert-token"
alert_timeout = "5s"
```

Check the posture before running anything:

```bash
./bin/factory doctor        # prints the hardening posture and every warning
./bin/factory threats       # aggregate hardening evidence across runs
./bin/factory threats --json --limit 0
```

Terminal states that mean "stopped, not built":

| State | Meaning | Operator action |
| --- | --- | --- |
| `EGRESS_UNVERIFIED` | The sandbox reached the internet although the policy denied it, reached the cloud metadata endpoint, or ran as uid 0 under `require_non_root`. | Fix the template or the egress policy. Do not re-run until `factory doctor` is clean. |
| `QUARANTINED` | The behavior monitor found indicators of out-of-bounds activity. | Read `hardening/behavior.json`; treat the sandbox as compromised and rotate anything it could reach. |
| `GATE_TAMPERED` | The tree modified a build or test script that judges it. This can happen with no trace in the patch (`git update-index --assume-unchanged`, a committed tamper), which is why the check reads the tree. | Review the tree by hand; do not accept the gate result. |
| `BLOCKED` | The agent reported it could not satisfy the task. | Not a security event: fix the task or the missing prerequisite. |
| `REJECTED` | Every gate passed but a human reviewer rejected the change. | Rework with `factory run --change <id> --parent-run <run-id>`. |

The agent's stop signal is `blocked.json` in the factory metadata directory
(`/workspace/.factory/blocked.json`), containing `{"reason": "..."}` or plain
text. Document it in the task text you give an agent, so it knows the honest
exit exists.

The console also serves the read-only connector view at
`/api/v1/console/connectors`. It reports configured connectors, their pipelines
and scopes, and the last recorded liveness observation. It never constructs a
connector, reads a token file, or exposes a credential.

## 6b. Connector, publication and evidence runbooks

The work-item pipeline adds a forge connector and a host-side publisher. Both
run outside every Cube sandbox: the connector holds a forge credential, and the
publisher reads the signed evidence bundle from the artifact store. Neither
credential is reachable from an agent.

### Webhook credential

The webhook receiver authenticates service-hook deliveries with
`webhook_username` and `webhook_secret_file`. They are a dedicated inbound
credential: `token_file` remains the outbound credential for API calls and git
pushes and is never accepted for an inbound delivery.

```toml
[connectors.azuredevops]
kind                = "azuredevops"
base_url            = "https://dev.azure.com/acme"
token_file          = "/etc/factory/secrets/azuredevops-token"
webhook_listen      = "127.0.0.1:8091"
webhook_username    = "esf-hooks"
webhook_secret_file = "/etc/factory/secrets/azuredevops-webhook"
```

Write the inbound secret to a private file owned by `factory` with mode `0600`.
`factory connector` refuses to start, and `factory doctor --profile production`
fails, when a connector configures `webhook_listen` without the pair or when
the file fails the private-file rules. A connector with neither `webhook_listen`
nor the inbound pair is sweep-only: it reconciles work items and binds no
listener, so its configuration needs no change. Rotate the inbound secret by
replacing the file and restarting the connector; the PAT does not need to be
rotated.

### Metrics and alerts

The factory exports these instruments through the configured OTLP endpoint
(`[observability]`), alongside the existing `factory_runs_*` instruments:

| Instrument | Meaning |
| --- | --- |
| `factory_connector_last_successful_sweep_timestamp_seconds` | Unix time of the last reconciliation sweep that completed and saved its watermark |
| `factory_connector_last_delivery_timestamp_seconds` | Unix time of the last authenticated webhook delivery |
| `factory_connector_subscription_state` | One-hot forge subscription state, labelled `factory.subscription.state` (`active`, `disabled`, `probationary`, `unknown`) |
| `factory_publication_failures_total` | Failed publication attempts, labelled `factory.failure.class` |
| `factory_evidence_failures_total` | Evidence signing or verification failures, labelled `factory.failure.class` |
| `factory_scope_queue_depth` | Work items still pending intake, counted from durable intake records without an outcome and grouped by their resolved scope, labelled `factory.scope`. Records written before scopes were persisted contribute no series |

Connector health is evaluated from the latest attempt, not from the newest
successful timestamps alone. A failed sweep attempt degrades the connector
immediately with reason `sweep_failed`, including a sweep whose reconciliation
finished but whose watermark could not be committed: reconciliation is only
successful once its cursor is durable. A failed delivery attempt also degrades
immediately with reason `delivery_failed`, because fresh successful timestamps
can otherwise hide a stalled delivery path. A reconciliation pass whose delivery
fails still records the subscription state and durable queue depth alongside
that failure, so `factory_scope_queue_depth` and the console snapshot keep
updating while intake is down. A successful sweep and an authenticated delivery
each stay fresh for three sweep intervals, so with the default one-minute
cadence a healthy connector is not degraded just because a later sweep succeeded
after its last delivery. A delivery may therefore be older than the newest
successful sweep and still count as current. A fresh successful sweep with no
delivery ever observed is healthy, with reason `sweep_only`: a sweep-only
connector, or one that has seen no work, proves reconciliation without a webhook
delivery.

The degraded reasons are `sweep_failed`, `delivery_failed`, `stale_sweep`,
`no_successful_sweep` (a delivery was observed but no successful sweep),
`no_recent_delivery`, `subscription_disabled` and `subscription_probationary`.
A connector with no successful sweep, no delivery, and no failed attempt is
`unknown` with reason `no_observations`, not degraded.

Health is latest-attempt-wins: each observation replaces the connector's durable
state, so a failed delivery stays visible until the next observation replaces
it, including a later successful sweep observation. A failed delivery is not
held until a successful delivery arrives. A webhook request the receiver rejects
before authenticating (HTTP 401) is not a delivery attempt: it never becomes the
replacing observation and leaves connector health unchanged.

A connector raises `connector.degraded` when it enters the degraded state or its
reason changes; a connector that stays degraded with the same reason does not
re-alert on every sweep, and a degradation after recovery alerts again.
Publication failures raise `publication.failed` or `publication.tampered`; an
evidence signing failure raises `evidence.failed`. Alerts are best-effort: a
delivery failure is recorded under `hardening.alerts` in `run.json` (or the
connector health snapshot) and never fails a run or a sweep. The read-only
console shows the same connector state at `/api/v1/console/connectors`.

### Connector outage or missed deliveries

Symptom: no new runs, and the console or alert reports `degraded` with reason
`sweep_failed`, `delivery_failed`, `stale_sweep`, `no_successful_sweep`, or
`no_recent_delivery`.

1. Confirm the connector process is running and can reach the forge base URL.
   `factory doctor --profile production` reads the durable connector-health
   snapshot and reports each connector's status, reason and last-success age;
   add `--offline` to skip the Cube and Temporal probes.
2. Check the connector log for HTTP 401, 403, 429, and DNS/TLS errors. A failed
   sweep deliberately does not advance
   `factory_connector_last_successful_sweep_timestamp_seconds`, and once the last
   success is older than three sweep intervals health is stale even when no newer
   delivery arrived.
3. Do not re-register the webhook first. Reconciliation is the primary intake
   path: the next successful sweep reads the durable watermark and recovers every
   missed revision.
4. After the forge is reachable, restart the connector if needed and
   confirm the sweep timestamp advances. A webhook is an accelerator, not the
   source of truth.

### Disabled or probationary subscription

Azure DevOps disables a service-hook subscription when its creator leaves the
organization, when the subscription is idle for six months, or when a trial or
probation ends. Other forges have equivalent disablement paths.

Symptom: `factory_connector_subscription_state` reports `disabled` or
`probationary`, and the connector health reason is `subscription_disabled` or
`subscription_probationary` (alert severity critical).

1. Re-enable or recreate the subscription with an operator-owned service
   principal or app installation, not a personal account.
2. Send the forge's test event and confirm
   `factory_connector_last_delivery_timestamp_seconds` advances.
3. Run a sweep and confirm the watermark advances. Missed events are recovered
   from the work-item revisions feed; no work is lost while the subscription is
   disabled.
4. If the subscription cannot be re-enabled, leave reconciliation running and
   record the reduced delivery path in the deployment notes.

### Token expiry or rotation

Symptom: the forge returns 401/403, sweeps stop, publication fails with
`connector_unavailable`, or the connector health is degraded.

1. Confirm the token file is an absolute path to a private regular file: no
   symlink, owner-only permissions, non-empty bounded content. A path with a
   missing or group-readable file fails closed at connector construction.
2. Rotate the token with the forge, write the new value to the private file
   atomically, and restart the connector. Never paste the value into
   configuration, a command line, or a sandbox.
3. Confirm one successful sweep and one delivery before considering the
   rotation complete. Publication uses the same operator connector, so a stale
   token also fails `publication.failed` with class `connector_unavailable`.

A PAT whose owner leaves the organization is an expiry waiting to happen. Use a
service principal or app installation, and keep the PAT path only as the
documented fallback.

Inbound webhook authentication uses `webhook_secret_file`, not the PAT. Rotate
that file independently: a PAT rotation does not change it, and rotating the
inbound secret does not require a new PAT.

### Publication throttle

Symptom: `factory_publication_failures_total{factory.failure.class="forge_failed"}`
increases, `publication.failed` alerts repeat, and the forge log shows HTTP 429.

1. Let the publisher honour the forge's retry hint. Publication is idempotent by
   change and revision: a retry updates the same branch and pull request instead
   of creating a second one.
2. Reduce concurrent intake for the affected scope; `factory_scope_queue_depth`
   shows the queued work. Do not delete the branch or close the draft pull
   request to force a retry.
3. If the failure is `status_failed`, the pull request exists. Re-run the
   publication retry to update the status; do not open a second pull request.
4. If the forge reports a body-limit failure (`plan_failed`), the body cap is
   smaller than the digest and summary. Correct the connector capability and
   retry; never trim the digest.

### Evidence verification failure

Symptom: the run ends `EVIDENCE_FAILED`; `evidence.failed` alert;
`factory_evidence_failures_total`; or publication fails with
`evidence_missing`/`evidence_mismatch` and a `publication.tampered` alert. No
pull request is published in any of these cases.

1. Read `error` and `conditions` in `run.json`, then verify the bundle offline:

   ```bash
   ./bin/factory verify --bundle .factory/runs/<run-id>/evidence/bundle.json \
     --key /etc/factory/evidence-public.pem
   ```

2. A missing or unreadable signing key is an operator configuration failure:
   restore the key through the private-file rules and rerun the change. Do not
   publish a run whose bundle is missing.
3. A signature or digest mismatch is a security event. Preserve the run
   directory, compare the bundle against `run.json`, and do not retry
   publication until the cause is understood. The agent has no key and no
   bundle-write path outside the sandbox.
4. Evidence verification never falls back to the patch or agent output. A
   failed verification stays failed.

### Signing key rotation

1. Generate a new ed25519 key pair on the factory host, outside every sandbox.
2. Publish the new public key alongside the old one for the overlap period, so
   bundles signed before the rotation still verify.
3. Replace `[evidence] signing_key_file` atomically with the new PKCS#8 key,
   keep the file owner-only, and restart the worker.
4. Confirm the next successful run records `EvidenceSigned=True`, then verify a
   new bundle with `factory verify --bundle ... --key ...`.
5. Retire the old private key only after the retention period for bundles it
   signed. Keep the old public key with the archived bundles. If a private key
   was exposed, stop publication, rotate immediately, and treat every bundle
   signed with the exposed key as untrusted.

### Retention of evidence bundles and signatures

`factory retention preview` and `factory retention apply` remain deliberately
narrow: they select only orphaned `.artifact-*` temporary files left by
interrupted atomic writes. Completed evidence is never eligible, including
`run.json`, `changes.patch`, logs, change records, `evidence/bundle.json`, and
`evidence/signature.json`.

The bundle and its detached signature are the durable proof a developer or
auditor checks, so keep them for at least as long as the run manifest, audit
log, and the organization's review period. Include them in every artifact-store
snapshot and restore drill. Deleting completed evidence requires a separate,
reviewed retention policy; the factory has no automatic deletion.

Delete an interrupted `.artifact-*` file only after confirming no active writer
owns it. A bundle that is present but does not verify is a failure to
investigate, not a file to prune.

## 7. How cleanup works

1. The sandbox ID is recorded as soon as it is created.
2. Cleanup is registered **before** creation, so every exit path is covered:
   success, agent failure, verification failure, timeout, cancellation.
3. `DestroySandbox` is retried up to 8 times with backoff — destroy is
   idempotent, and a leaked microVM is far more expensive than a retry.
4. After destroying, the activity **independently verifies** the sandbox is gone
   by listing live sandboxes, rather than trusting the destroy call.
5. The outcome is written to `cleanup.json` and to `run.json`.
6. The manifest is written **after** cleanup, so it always contains the result.

### Detecting leaked sandboxes

```bash
./bin/factory sandboxes
```

The command exits non-zero when any sandbox carries `origin=factory`. Every
factory sandbox is tagged with `origin`, `run_id`, `harness`, and `creator`, so a
leak is attributable to a specific run. The `sandbox_leaks_total` metric counts
them.

## 8. Rotating credentials

No Cube credential is required on this deployment (authentication is disabled).
If it is enabled:

```bash
# .env (gitignored) or the deployment environment
CUBE_API_KEY=<new-value>
```

Then restart the worker. `CUBE_API_KEY` is never logged — `cube.Config.String()`
renders it as `set(redacted)` — and is never written to any artifact.

For an agent provider credential, the factory supports two mechanisms:

| Mechanism | Config | Notes |
| --- | --- | --- |
| Environment allowlist | `pass_env = ["MY_TOKEN"]` | The variable is forwarded to the agent process only if its name is listed |
| Provisioned provider file | `provider_files = { "<sandbox path>" = "<host path>" }` | Opt-in. The file is copied into the sandbox so the agent can authenticate. |
| CubeEgress vault (Unreal) | `credential_mode = "cube_egress"`, `api_key_file = "..."` | Recommended. HTTPS header injection keeps the real key outside the microVM and applies runtime default-deny egress. |

Both are operator configuration. Neither is reachable from a request. When a
provider file is configured, its contents are also registered with the redactor,
so they cannot be persisted into evidence.

> **Trade-off, stated plainly:** environment values and provisioned provider
> files are readable by the agent. The Unreal harness's `cube_egress` mode avoids
> that exposure. It requires CubeEgress and its CA in the selected template;
> keep `runtime_allow_out` minimal and monitor CubeEgress's metadata audit log.

To rotate a provisioned provider file, replace the host file and restart the
worker; the new contents are staged on the next run and re-registered with the
redactor.

To rotate a CubeEgress-managed key, update the worker's secret source and
restart the worker. Existing sandboxes retain their per-run policy until they
are destroyed; new runs receive the rotated value.

## 9. Adding a new agent harness

Add a block to `factory.toml` and restart the worker.

```toml
[harnesses.codex]
type        = "generic"
executable  = "codex"
args        = ["exec", "{{model_args}}", "--full-auto"]
model_flag  = "--model"
model       = "gpt-5.6-luna"
timeout     = "45m"
packages    = ["git"]
prompt_mode = "stdin_file"
pass_env    = ["OPENAI_API_KEY"]
```

| Field | Meaning |
| --- | --- |
| `type` | `opencode`, `unreal` (built-in safe adapters), or `generic` |
| `executable` | Operator-owned program name — never caller-supplied |
| `args` | Fixed argument vector. `{{model_args}}` expands to the model flag + model, or disappears |
| `model_flag` | The agent's model-selection flag |
| `packages` | Extra OS packages for this harness |
| `prompt_mode` | `stdin_file` (default, no interpolation) or `argv` |
| `pass_env` | Host variables allowed through to the agent. Nothing else passes |
| `binary` | Host path to a standalone binary to push into the sandbox |
| `catalog_cache` | Host path to a model catalog, staged to make model resolution offline |
| `provider_files` | In-sandbox path → host path, staged for agent authentication |

Notes:

- `{{model_args}}` is required to appear **after** the subcommand for CLIs like
  `opencode2 run --model X`. A fixed insertion point cannot express both shapes.
- The factory installs `sandbox.base_packages` (default `git`,
  `ca-certificates`) in every sandbox regardless of harness, because repository
  preparation needs them.
- The harness is selected by **name** (`--agent codex`). A request cannot name an
  executable.
- Dynamic placeholders must be complete argv elements and are rejected inside
  a shell `-c` program. Use `{{model_args}}` for model selection.

Verify with `./bin/factory doctor`, which lists registered harnesses.

For the async `unreal-agent` runner (standalone binary, no Go dependency, full
operator contract + evidence mapping), see [harness-unreal](harness-unreal.md)
and `scripts/unreal-demo.sh`.

## 10. Adding a verification profile

```toml
[verification.strict]
name = "strict"

[[verification.strict.steps]]
id          = "fmt"
argv        = ["gofmt", "-l", "."]
mandatory   = true
timeout     = "2m"

[[verification.strict.steps]]
id          = "build"
argv        = ["./build.sh"]
mandatory   = true
timeout     = "10m"

[[verification.strict.steps]]
id          = "unit-tests"
argv        = ["go", "test", "./..."]
mandatory   = true
timeout     = "20m"
dir         = "backend"      # relative to the repository root

[[verification.strict.steps]]
id          = "lint"
argv        = ["golangci-lint", "run"]
mandatory   = false          # advisory: recorded but does not gate
```

Rules:

- **Always a structured `argv`.** There is no shell-string form, by design.
- `mandatory` defaults to `true`. An opt-out gate is the surprising case.
- `dir` must be relative and must not contain `..`.
- The factory result is `PASSED` only when **every mandatory step exits zero**.
- Output is captured per gate and redacted before storage.

Run a profile with `--verification strict`.

## 10a. Sandbox environment setup

Some toolchains cannot be expressed as OS packages — a language runtime's
dependencies, for example. `sandbox.setup_script` is factory-owned shell code
that runs after `base_packages` and **before** the repository is cloned, so it
can prepare the image but cannot touch the checkout.

```toml
[sandbox]
base_packages = ["git", "ca-certificates", "python3-pip"]
setup_script = """
set -eu
export DEBIAN_FRONTEND=noninteractive
python3 -m pip install --break-system-packages -q setuptools pytest pydantic PyYAML jsonschema
echo sandbox-setup-ok
"""
```

It is OPERATOR configuration and must never contain task-derived text. Anything
that needs the checkout (such as `pip install -e .` for a src-layout Python
project) belongs in a verification gate, which runs after the clone.

## 10b. Verification that mutates the working tree

The deliverable patch is captured **immediately after the agent runs** and
before any gate executes. If a gate rewrites a tracked file — a build step
regenerating a lockfile, for example — the factory does not fold that into the
patch. It records the drift separately and warns:

```
WARNING: a verification gate modified the working tree.
         The deliverable patch is captured BEFORE verification, so that
         change is not in changes.patch.
         Evidence: <run-id>/verification/post-verification.patch
```

The manifest carries `verification_mutated_tree` and `post_verification_patch`.
If a gate routinely mutates the tree, make it side-effect free or move the
mutation into the build step — the factory will keep telling you.

## 11. Resource limits

Configured under `[limits]`:

| Setting | Default | Enforced |
| --- | --- | --- |
| `agent_timeout` | 30m | Agent command timeout and activity deadline |
| `verification_timeout` | 15m | Whole verification activity context, plus per-gate timeouts |
| `total_timeout` | 90m | Durable workflow timer for execution; cleanup runs afterwards |
| `sandbox_idle_timeout` | 60m | Cube idle timeout |

A runaway agent cannot consume unlimited resources: it is bounded by the agent
timeout, the activity timeout, and the sandbox idle TTL independently.

## 12. Troubleshooting

| Symptom | Likely cause | Action |
| --- | --- | --- |
| `ERR_PROXY_CONNECTION_FAILED` / connection refused on `:4000` | Wrong Cube port, or Cube not running | `curl http://127.0.0.1:4000/health`; check `systemctl status cube-sandbox-cube-api` |
| `template not found` | Template id changed or was deleted | `cubemastercli tpl list`; update `cube.template_id` |
| `template is required` | `cube.template_id` unset | Set `CUBE_TEMPLATE_ID` or the config file |
| `git: command not found` during `repo.prepare` | `sandbox.base_packages` was emptied | Restore `["git", "ca-certificates"]` |
| Agent exits 1 with `Model unavailable` | The agent's provider is not authenticated inside the sandbox | Provision a credential (`pass_env` or `provider_files`); see §8 |
| `INFRASTRUCTURE_FAILED` at `cube.create` | Cube API unreachable or the template is not READY | `factory doctor` |
| `factory sandboxes` exits non-zero | A sandbox leaked | Inspect the `run_id` metadata and the run's `cleanup.json` |
| Worker idle, run never starts | Task queue mismatch, or no worker running | Compare `temporal.task_queue` in config with the worker log; `factory worker` |

## 13. Development commands

```bash
make build              # factory + discovery binaries into ./bin
make test               # unit tests (no Cube, no Temporal)
make lint               # gofmt, go vet, shellcheck
make verify             # lint + test + cube-smoke

make temporal-up        # Temporal + PostgreSQL + UI, loopback only
make temporal-status
make temporal-hello
make temporal-down

make cube-smoke         # prove the host can use the existing CubeSandbox
make cube-netprobe      # discover the deployment's egress behaviour
make cube-agent-spike   # prove a coding agent runs inside a microVM
make cube-sandboxes     # leak check

make integration-test   # Cube + Temporal integration suites
make factory-doctor
make factory-worker
make factory-run-demo   # the Phase 1 acceptance test
```
