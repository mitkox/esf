# Deploying the factory appliance

The 1.0.0-rc.1 candidate requires an attested companion qualification manifest
for the exact source and built assets before production use. It is a release candidate: the
outstanding GA gates in the
[1.0.0-rc.1 release notes](release-v1.0.0-rc.1.md) remain open. This guide
describes the deployment configuration; it does not qualify a deployment.
Complete the live, security, backup, restore, migration, load and soak checks in
those release notes. Input versions remain recorded in the
[release inventory](../release/inventory.json).

The target deployment uses an operator-managed Temporal cluster, CubeSandbox,
and durable local or block-backed storage on a dedicated factory host. Run one
authoritative deployment and drain it for upgrades; HA and rolling upgrades are
unsupported. Cube template resources
and egress policy must be set by the operator; the provider does not enforce the
optional CPU/memory/disk fields in the factory configuration.

The appliance runs one `factory` binary in three service roles:

- the Temporal worker, which executes runs and forge publication activities;
- the execution API, which accepts the versioned `/v1` contract over an
  authenticated listener; and
- the connector host, which is a worker process hosting the host-side forge
  adapters. Run it only when you want a dedicated connector worker; the normal
  worker already hosts connector activities.

All three share `/var/lib/factory`, `/etc/factory/factory.toml`, and the
operator-owned credential files. Keep one authoritative deployment.

## Configuration and credentials

Start with `factory.example.toml`. Set the actual Cube endpoint and template,
repository allowlist, harness binary, credentials and verification commands.
For services using the units in `deploy/`, use absolute paths under `/etc/factory`
for configuration and provisioned files, and set
`storage.data_dir = "/var/lib/factory"`. The services cannot read home
directories.

### Execution API

Generate a bearer token and enable the listener:

```sh
sudo install -d -m 0700 /etc/factory
sudo sh -c 'umask 077; openssl rand -hex 32 > /etc/factory/api.token'
sudo chown factory:factory /etc/factory/api.token
```

```toml
[api]
enabled = true
listen = "0.0.0.0:8090"
token_file = "/etc/factory/api.token"
tls_cert_file = "/etc/factory/tls/api.crt"
tls_key_file = "/etc/factory/tls/api.key"
```

The token must contain at least 32 characters from the HTTP bearer alphabet.
The token file and the TLS private key must be regular files with mode `0600`
and no group or other access; `factory doctor --profile production` and API
startup refuse anything else. A loopback listener may use plain HTTP behind a
TLS-terminating proxy. Any non-loopback listener requires native TLS, with both
files set together. The Kubernetes chart mounts an optional TLS Secret at
`/etc/esf/tls` and exposes the listener through a Service.

### Forge connectors and pipelines

A connector is a host-side adapter; its credential is a private-file reference,
never a value in the configuration:

```toml
[connectors.azuredevops]
kind       = "azuredevops"
base_url   = "https://dev.azure.com/acme"
token_file = "/etc/factory/secrets/azuredevops-token"

[pipelines.bugfix]
connector = "azuredevops"

[[pipelines.bugfix.rules]]
project              = "Payments"
area                 = "Payments\\Bugs"
label                = "esf"
type                 = "Bug"
state                = "Active"
scope                = "payments"
repository           = "https://dev.azure.com/acme/payments/_git/payments"
base_branch          = "main"
verification_profile = "default"
```

Create the token as a regular file owned by `factory` with mode `0600`; the
worker restarts and rejects it if it is group- or other-readable, a symlink, or
unavailable. The token is read only on the host and is never injected into a
Cube sandbox.

### Evidence signing key

Publication requires a host-held PKCS#8 ed25519 signing key outside every Cube
sandbox:

```sh
sudo install -d -m 0700 /etc/factory/secrets
sudo sh -c 'umask 077; openssl genpkey -algorithm ED25519 -out /etc/factory/secrets/evidence-signing-key.pem'
sudo chown factory:factory /etc/factory/secrets/evidence-signing-key.pem
```

```toml
[evidence]
signing_key_file = "/etc/factory/secrets/evidence-signing-key.pem"
```

`factory doctor --profile production` fails when the key is absent, group- or
other-readable, not a regular file, or not a PKCS#8 ed25519 private key. Back up
the key with the matching configuration; losing it prevents signing new
evidence, and publishing it lets anyone forge a bundle.

For remote Temporal:

```toml
[temporal]
host_port = "temporal.example.com:7233"
namespace = "factory"
task_queue = "factory"
tls = true
server_name = "temporal.example.com"
ca_file = "/etc/factory/temporal-ca.pem"
cert_file = "/etc/factory/client.pem"
key_file = "/etc/factory/client-key.pem"
payload_keyring = "/etc/factory/payload-keys.json"
```

Omit `ca_file` to use system certificate roots. Client certificate and key are
optional if the server uses another authentication mechanism. For API-key
authentication set `api_key_env = "TEMPORAL_API_KEY"` and supply its value in
`/etc/factory/worker.env`. Plaintext connections are accepted only on loopback,
and cannot carry API-key authentication. Certificate verification is mandatory.

Use the same payload keyring on **every submitting CLI and worker**. Generate the
keyring once, outside Git, and distribute it through your secret-management
system. Its format is:

```json
{"active":"key-2026-09","keys":{"key-2026-09":"BASE64_OF_32_RANDOM_BYTES"}}
```

Generate a real file without printing the key:

```sh
umask 077
python3 - <<'PY'
import base64, json, secrets
with open("payload-keys.json", "x") as f:
    json.dump({"active": "key-2026-09", "keys": {
        "key-2026-09": base64.b64encode(secrets.token_bytes(32)).decode()
    }}, f)
PY
```

Payloads use authenticated AES-256-GCM with random nonces. Failure messages are
encoded and encrypted too. Workflow IDs, routing metadata and server-side timing
remain visible. Historical plaintext is still readable and is not retroactively
encrypted. Artifact storage remains plaintext with restricted file permissions;
use encrypted volumes/backups when required. Provider credentials staged into a
sandbox are readable by the agent.

To rotate, first add the new key to all keyrings while retaining the old active
key. Restart clients/workers so all can decrypt both keys. Then change `active`
and restart again. Retain old keys for at least the history and backup lifetime.
Losing a required key makes that history unrecoverable.

For Unreal provider keys, use `credential_mode = "cube_egress"` with a
file-backed secret. A systemd drop-in can load the source without placing its
value in `worker.env`:

```ini
[Service]
LoadCredential=unreal-provider-key:/etc/factory/secrets/openrouter-key
```

Then configure:

```toml
[harnesses.unreal]
credential_mode = "cube_egress"
api_key_file = "/run/credentials/factory-worker.service/unreal-provider-key"
```

The source file should be root-owned and mode `0600`; systemd exposes the
credential read-only to the service. A Vault Agent-rendered owner-only file can
be used at the same `api_key_file` seam. The worker reads it only while applying
the per-run CubeEgress policy, and the value does not enter Temporal or the VM.

## Optional DSPy/Jev intake advisory

Install the pinned host-side Python component into an isolated environment:

```sh
python3.12 -m venv /opt/factory/intake-venv
/opt/factory/intake-venv/bin/pip install /path/to/esf/tools/intake
```

Create `/etc/factory/secrets/typesafe-api-key` outside Git, owned by root with
mode `0600`. Do not paste the key into `factory.toml`, `worker.env`, a shell
command, or a task. For a manual setup, create the file and enter its value in
an editor so the key does not enter shell history:

```sh
sudo install -d -m 0700 /etc/factory/secrets
sudo install -o root -g root -m 0600 /dev/null /etc/factory/secrets/typesafe-api-key
sudoedit /etc/factory/secrets/typesafe-api-key
```

Install the optional [intake drop-in](../deploy/factory-worker.service.d/intake.conf)
when enabling intake:

```sh
sudo install -d -m 0755 /etc/systemd/system/factory-worker.service.d
sudo install -m 0644 deploy/factory-worker.service.d/intake.conf \
  /etc/systemd/system/factory-worker.service.d/intake.conf
sudo systemctl daemon-reload
```

Then enable the advisory in the operator config:

```toml
[intake]
enabled = true
python_executable = "/opt/factory/intake-venv/bin/python"
key_file = "/run/credentials/factory-worker.service/typesafe-api-key"
model = "jev-latest"
timeout = "15s"
```

The worker sends only the full submitted task text to TypeSafe. The Python
process reads the systemd credential copy, and no key enters Temporal history,
the sandbox, the task prompt, or durable evidence. An outage records an
`unavailable` advisory and leaves the run's existing result rules intact.
Raw probabilities remain uncalibrated evidence. ReAnchor fits the decision
threshold, score cuts and choice weights; it does not calibrate the raw
probabilities. Activate a compiled program only after held-out review.
For local development, keep a `0600` key file outside the repository and set
`key_file` to its absolute path.

To use a local TypeSafe-compatible service, set `intake.base_url` to its origin,
for example `"http://127.0.0.1:10000"`, and set `intake.model` to the model
served there. The client still requires a nonempty owner-only `key_file`; a
local service that ignores authentication can use a placeholder credential.
HTTP is accepted only for loopback hosts. Review and recalibrate any saved
intake program before using it with a different model.

To calibrate later, manually review at least 40 distinct task outcomes and write
an owner-only JSONL file outside Git. Each line has `run_id`, `task`, `ready`
(Boolean), `task_type` (`bugfix`, `feature`, `refactor`, `docs`, or `other`), and
`ambiguity` (`clear`, `partial`, or `ambiguous`). Label a run from its saved
`task.json` without using the TypeSafe key:

```sh
sudo -u factory /opt/factory/intake-venv/bin/python -m esf_intake label \
  --run-dir /var/lib/factory/runs/RUN_ID \
  --labels /var/lib/factory/intake/labels.jsonl \
  --ready yes --task-type bugfix --ambiguity clear
```

After collecting the reviewed labels, run calibration as a transient systemd
service. It receives its own credential copy; a shell outside the worker unit
cannot read `/run/credentials/factory-worker.service/`:

```sh
sudo systemd-run --quiet --wait --pipe --collect --uid=factory \
  -p LoadCredential=typesafe-api-key:/etc/factory/secrets/typesafe-api-key \
  -p WorkingDirectory=/var/lib/factory \
  /bin/sh -c 'TYPESAFE_API_KEY_FILE="$CREDENTIALS_DIRECTORY/typesafe-api-key" exec /opt/factory/intake-venv/bin/python -m esf_intake calibrate --labels /var/lib/factory/intake/labels.jsonl --output /var/lib/factory/intake/calibrated-v1.json'
```

The command reports baseline and candidate scores on held-out tasks and writes
the program only if it improves the metric without more false-ready decisions.
It does not activate the program. To promote it, set `intake.program_path` to
the saved JSON path and restart the worker. Keep the program operator-owned;
recorded run manifests identify its SHA-256 digest.

## Install the appliance

1. Build with the version in `go.mod`: `go build -trimpath -o bin/factory ./cmd/factory`.
   The release installer accepts `ESF_COMPONENT=factory`, `api`, `connector`, or
   `appliance` and installs the same binary for every role.
2. Create a dedicated `factory` system user/group. Install the binary at
   `/usr/local/bin/factory` and configuration at `/etc/factory/factory.toml`.
3. Configuration may be `root:factory` mode `0640`. Payload keyrings, API token
   files, connector token files, TLS private keys and the evidence signing key
   must instead be regular files with no group/other access (mode `0600`),
   readable by the service identity. Use service-owned files or systemd
   `LoadCredential`; keep credential source files root-only. Symlinks and
   oversized secret files are refused. Before upgrading an existing
   installation, migrate any `0640` keyrings to this ownership/permission model
   on every submitting CLI and service, then run doctor as that identity before
   restarting. Create `/etc/factory/worker.env` even without environment secrets;
   `factory-api.service` and `factory-connector.service` read the same file.
4. Run `factory doctor --profile production --config /etc/factory/factory.toml`
   as the service user with the same environment. Resolve every reported
   failure, including the API listener, connector configuration, token-file
   permissions, and evidence signing key checks. Use `--offline` when Cube and
   Temporal are intentionally unreachable.
5. Install the units for the roles you run:

   ```sh
   sudo install -m 0644 deploy/factory-worker.service /etc/systemd/system/
   sudo install -m 0644 deploy/factory-api.service /etc/systemd/system/
   sudo install -m 0644 deploy/factory-connector.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl enable --now factory-worker factory-api
   ```

   `factory-worker` and `factory-connector` both run the Temporal worker and
   host forge adapter activities. Start one, or both when you intend to scale
   worker concurrency; they use the same task queue and configuration.

   The release installer can copy the selected units when run from a checkout:
   `sudo ESF_COMPONENT=appliance ESF_SYSTEMD_DIR=/etc/systemd/system ./install.sh`.
   It never starts or enables a service; run `systemctl daemon-reload` and
   enable the roles you intend to operate.

The units use restricted filesystem access, a private temporary directory,
automatic restart after failure, and SIGTERM shutdown. The worker stops polling
and gives activities 30 seconds to finish before stopping. The API drains
in-flight requests on shutdown. Temporal resumes outstanding work when a worker
returns. Agent execution is not automatically retried because an
acknowledgement can be lost after the agent finishes.

Drain active runs before upgrading from earlier workflow code. Stop the API and
connector units with the worker, promote the new binary, configuration, and
credentials, run `factory config migrate --dry-run` and
`factory doctor --profile production`, then start the units again. Validate
replay against retained histories before attempting a rolling upgrade across
versions.

## Kubernetes appliance

`deploy/helm/esf` renders the same appliance: one factory worker Deployment, an
execution API Deployment and Service, and one connector worker Deployment.
Every workload uses the digest-pinned image, the restricted non-root security
context, a single replica with the `Recreate` strategy, and
`automountServiceAccountToken: false`. Both worker Deployments use the same task
queue and configuration, so disable one when a single worker is sufficient. The
NetworkPolicy denies all ingress except the API Service port and keeps egress
limited to DNS plus the CIDRs and ports you list.

```sh
helm upgrade --install esf deploy/helm/esf -f values.yaml
```

Provide `factory.configSecret` with the same `[api]`, `[connectors.*]`,
`[pipelines.*]`, and `[evidence]` sections. Set `api.port` to the listener port.
If the API listens on a non-loopback address, set `api.tlsSecret` to a Secret
containing `tls.crt` and `tls.key`; otherwise keep the listener loopback-only
behind a TLS proxy. Put the API and connector tokens and the signing key in
`factory.credentialSecret`; an init container stages them as `0600` files under
`/etc/esf/credentials`.

The chart's workloads mount one `ReadWriteOnce` claim. The API and connector
pods are pinned to the factory worker's node, and the chart requires
`factory.enabled=true`; provide a block-backed claim that permits the
co-located mounts, and keep exactly one replica per workload. The chart remains outside the
Kubernetes/Helm qualification gate: installing it does not qualify a cluster or
close that gate.

Upgrade by scaling the workloads to zero, snapshotting the claim, Temporal, the
Secrets, and the values file, verifying the image digest, then running
`helm upgrade`. Recreate strategy starts one authoritative pod per workload; do
not use rolling or multiple replicas. Restore into an isolated namespace with
the matching PVC and key material before trusting a rollback.

## Operations and recovery

- Alert on failed runs, unverified cleanup, disk exhaustion, missing worker or
  connector pollers, API listener failures, and Temporal queue latency. Export
  the existing OTLP metrics to the deployment's collector and retain service
  logs.
- Sandbox ownership includes the Temporal execution ID. Creation recovers an
  existing matching sandbox after a lost response; retries do not create another
  VM when the first create is unresolved. Cleanup searches ownership metadata
  even when no sandbox ID was returned. If Cube loses connectivity or delays
  visibility, inspect `factory sandboxes` and reconcile only the affected
  execution's resources after its workflow is terminal. Keep finite Cube TTLs.
- Evidence-write failures prevent a successful factory verdict without retrying
  a completed agent or verification pass. When the entire artifact store is
  unavailable, finalization retries and then fails the workflow visibly. Restore
  storage and use the retained Temporal history to investigate; do not blindly
  resubmit the same task to obtain missing evidence.
- Back up Temporal's database using its operator-supported backup method, the
  complete artifact filesystem, configuration, and the payload keyring. Include
  the API token, every connector token, the TLS private key, and the evidence
  signing key; the host backup below covers them through `/etc/factory`. Stop
  submissions and drain runs before a coordinated backup if your storage cannot
  take consistent snapshots. Keep encrypted key backups separately protected.
- Test restoration into an isolated namespace/cluster and a separate artifact
  directory, using the matching keyring. Retrieve a finished manifest and patch,
  then resume a deliberately interrupted disposable run. Never test restoration
  by overwriting the active deployment.

See [the readiness review](production-readiness.md) for executed validation.

## Upgrade, migration and rollback

Use local or block-backed storage for the factory evidence tree. NFS and other
shared network filesystems are unsupported. Before changing the factory binary,
stop new submissions, wait for active factory runs to reach a terminal state,
and reconcile sandbox cleanup. Then stop `factory-api`, `factory-connector`, and
`factory-worker` (skip units you do not run).

Use `factory storage usage --output json` to measure the durable evidence tree
and its run count before sizing or restoring the volume. This command reads
metadata only and leaves evidence intact.
`factory retention preview --older-than-days 30 --output json` identifies only
orphaned `.artifact-*` files left by interrupted atomic writes. If the exact
list is acceptable, pass its `plan_digest` to `factory retention apply
--older-than-days 30 --plan-digest DIGEST`. Apply rejects a changed plan and
never selects completed manifests, patches, logs, change records, signed
evidence bundles (`evidence/bundle.json`), detached signatures
(`evidence/signature.json`), or other evidence. There is no automatic deletion.
Plan a separate reviewed policy before deleting historical evidence; until
then, retain bundles and signatures with the run records and keep them in every
artifact-store backup and restore drill.
Set `limits.artifact_bytes` to cap each durable artifact write (32 MiB by
default). Oversized writes fail explicitly; use `limits.command_output_bytes`
for the separate bound applied before Cube command output reaches the SDK.

Take an offline snapshot of the complete factory evidence and the matching
configuration (when upgrading from a Machinist release and preserving console
state, also snapshot the console PVC). For the default VM paths, run the
following as root with a private destination directory:

```sh
install -d -m 0700 /var/backups/esf
tar --numeric-owner -C / -czf /var/backups/esf/pre-upgrade.tar.gz \
  var/lib/factory etc/factory
cd /var/backups/esf
sha256sum pre-upgrade.tar.gz > pre-upgrade.tar.gz.sha256
```

Back up Temporal with the operator's supported database procedure at the same
drained point. Keep the payload keyring with the matching application backup.
Run `factory config migrate --dry-run` and `factory doctor --profile production
--output json` before starting the new worker. Install and verify the pinned agents with `factory agents verify`.
Machinist state is not part of the factory: 0.6.2 neither reads nor migrates
it. On a host install it lives under the Machinist service user's home,
normally `/home/machinist/.machinist`; on Kubernetes it lived on the console
PVC.

To drill restoration, use a separate VM with no active services, copy the
snapshot there, verify its checksum, inspect its member paths, and restore:

```sh
cd /var/backups/esf
sha256sum -c pre-upgrade.tar.gz.sha256
tar -tzf pre-upgrade.tar.gz
tar --numeric-owner -C / -xzf pre-upgrade.tar.gz
```

Restore the matching Temporal snapshot and the matching older ESF binaries
before starting services. Never restore over a running service.
After any schema upgrade, rollback requires this matching snapshot; replacing
only the binary can corrupt or reject migrated state. Preserve the snapshot
through the full retention and audit review period.

### Removing Machinist

ESF 0.6.2 removed the Machinist CLI, local control plane and console. When
upgrading a host or cluster that still runs them:

1. Stop and disable `machinist-control-plane` and `machinist-worker`, then
   remove their unit files and binaries.
2. On Kubernetes, stop the old console and export/snapshot its console PVC (or
   `console.storage.existingClaim`) before upgrading; PVC deletion is
   irreversible. Then remove the `console` values and delete any console
   Deployment or Secret that your installation manages outside the chart;
   `helm upgrade` removes resources created by earlier chart versions.
3. On a host install, archive or delete the Machinist service user's state,
   normally `/home/machinist/.machinist`; the factory does not read that state.
4. Run `factory doctor --profile production` and a disposable run to confirm the
   factory remains healthy.
5. Install the factory archive only. `ESF_COMPONENT=machinist|combined` fails
   with guidance.

The Machinist control plane is gone, but the read-only operator console is not:
`factory console` serves the enterprise dashboard over the same durable
evidence (and optional bounded live Temporal/Cube reads). Keep it on loopback
behind TLS, or let a reverse proxy terminate TLS; see the
[console guide](console.md). CLI-first operators can continue to use
`factory describe`, `status --watch`, `logs` and `get changes`. See
[the 1.0.0-rc.1 release notes](release-v1.0.0-rc.1.md) and
[ADR 0008](adr/0008-remove-machinist.md).

### Local repository registration

Workers reject local sources unless their exact canonical directories appear in
`[repositories].local_paths`. Register Git worktree roots or bare repository
roots, never ordinary subdirectories. Descendants, prefix matches and symlink
aliases do not inherit authorization. Restart the worker after updating policy;
a caller's CLI configuration cannot authorize a path on the worker. Registration
exports all refs/history, so protect the directory, parents and Git metadata from
untrusted writers and register only histories suitable for the task's sandbox.
