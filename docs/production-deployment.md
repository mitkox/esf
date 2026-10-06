# Deploying the factory worker

The 0.6.1 candidate requires an attested companion qualification manifest for
the exact source and built assets before production use. This guide describes
the deployment configuration; it does not qualify a deployment. Complete the
live, security, backup, restore, migration, load and soak checks in the
[release procedure](release-v0.6.1.md). Input versions remain recorded in the
[release inventory](../release/inventory.json).

The target deployment uses an operator-managed Temporal cluster, CubeSandbox,
and durable local or block-backed storage on a dedicated factory host. Run one
authoritative deployment and drain it for upgrades; HA and rolling upgrades are
unsupported. Cube template resources
and egress policy must be set by the operator; the provider does not enforce the
optional CPU/memory/disk fields in the factory configuration.

## Configuration and credentials

Start with `factory.example.toml`. Set the actual Cube endpoint and template,
repository allowlist, harness binary, credentials and verification commands.
For a worker using `deploy/factory-worker.service`, use absolute paths under
`/etc/factory` for configuration and provisioned files, and set
`storage.data_dir = "/var/lib/factory"`. The service cannot read home directories.

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

## Install the worker

1. Build with the version in `go.mod`: `go build -trimpath -o bin/factory ./cmd/factory`.
2. Create a dedicated `factory` system user/group. Install the binary at
   `/usr/local/bin/factory` and configuration at `/etc/factory/factory.toml`.
3. Make configuration and keyrings readable only by root and the factory group
   (for example owner `root:factory`, mode `0640`). Keep any source loaded by
   `LoadCredential` root-only and mode `0600`. Create `/etc/factory/worker.env`,
   even when no environment credentials are needed.
4. Run `factory doctor --config /etc/factory/factory.toml` as the service user
   with the same environment. Resolve every reported failure.
5. Install `deploy/factory-worker.service` into `/etc/systemd/system/`, reload
   systemd, and enable/start `factory-worker`.

The service uses restricted filesystem access, a private temporary directory,
automatic restart after failure, and SIGTERM shutdown. The worker stops polling
and gives activities 30 seconds to finish before stopping. Temporal resumes
outstanding work when a worker returns. Agent execution is not automatically
retried because an acknowledgement can be lost after the agent finishes.

Drain active runs before upgrading from earlier workflow code. Validate replay
against retained histories before attempting a rolling upgrade across versions.

## Operations and recovery

- Alert on failed runs, unverified cleanup, disk exhaustion, missing worker
  pollers, and Temporal queue latency. Export the existing OTLP metrics to the
  deployment's collector and retain service logs.
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
  complete artifact filesystem, configuration, and the payload keyring. Stop
  submissions and drain runs before a coordinated backup if your storage cannot
  take consistent snapshots. Keep encrypted key backups separately protected.
- Test restoration into an isolated namespace/cluster and a separate artifact
  directory, using the matching keyring. Retrieve a finished manifest and patch,
  then resume a deliberately interrupted disposable run. Never test restoration
  by overwriting the active deployment.

See [the readiness review](production-readiness.md) for executed validation.

## v0.5.0 VM upgrade and rollback

The optional console factory view is read-only. Configure Machinist with
`[factory_read] root = "/var/lib/factory"` and a private
`token_file = "/etc/machinist/factory-read-token"` holding at least 32 random
characters. The token has a separate identity from the worker token and grants
no factory-write authority. Give only the Machinist control-plane account read
access to the factory evidence tree, for example through a dedicated read-only
ACL or a systemd drop-in with `SupplementaryGroups=factory`. The console
remains bound to loopback.
Its patch downloads require the read token and use attachment headers; previews
are rendered as text.

The console stores a disposable `factory-read-index.sqlite` beside its
Machinist database. It rebuilds the index from ordinary factory manifests at
startup and refreshes it when the runs or changes directory changes. Paginated
views read the index while individual manifests remain the source of truth.
If the index is damaged, stop the console, remove only that index file, and
restart it.
Use `factory storage usage --output json` to measure the durable evidence tree
and its run count before sizing or restoring the volume. This command reads
metadata only and leaves evidence intact.
`factory retention preview --older-than-days 30 --output json` identifies only
orphaned `.artifact-*` files left by interrupted atomic writes. If the exact
list is acceptable, pass its `plan_digest` to `factory retention apply
--older-than-days 30 --plan-digest DIGEST`. Apply rejects a changed plan and
never selects completed manifests, patches, logs, change records, or other evidence.
There is no automatic deletion. Plan a separate reviewed policy before
deleting historical evidence.
Set `limits.artifact_bytes` to cap each durable artifact write (32 MiB by
default). Oversized writes fail explicitly; use `limits.command_output_bytes`
for the separate bound applied before Cube command output reaches the SDK.

Use local or block-backed storage for the factory and Machinist SQLite files.
NFS and other shared network filesystems are unsupported. Before changing
either binary, stop new submissions, wait for active factory runs to reach a
terminal state, and reconcile sandbox cleanup. Then stop
`factory-worker`, `machinist-worker`, and `machinist-control-plane`.

Take an offline snapshot of the complete factory and Machinist state and the
matching configuration. For the default VM paths, run the following as root
with a private destination directory:

```sh
install -d -m 0700 /var/backups/esf
tar --numeric-owner -C / -czf /var/backups/esf/pre-v0.5.0.tar.gz \
  var/lib/factory home/machinist/.machinist etc/factory
cd /var/backups/esf
sha256sum pre-v0.5.0.tar.gz > pre-v0.5.0.tar.gz.sha256
```

Back up Temporal with the operator's supported database procedure at the same
drained point. Keep the payload keyring with the matching application backup.
Run `factory config migrate --dry-run` and `factory doctor --profile production
--output json` before starting the new worker. Install and verify the pinned agents with `factory agents verify`.
Machinist creates its own schema-2 backup before migrating to schema 5 and
refuses unknown populated databases.

To drill restoration, use a separate VM with no active services, copy the
snapshot there, verify its checksum, inspect its member paths, and restore:

```sh
cd /var/backups/esf
sha256sum -c pre-v0.5.0.tar.gz.sha256
tar -tzf pre-v0.5.0.tar.gz
tar --numeric-owner -C / -xzf pre-v0.5.0.tar.gz
```

Restore the matching Temporal snapshot and the matching older ESF binaries
before starting services. Never restore over a running service.
After any schema upgrade, rollback requires this matching snapshot; replacing
only the binary can corrupt or reject migrated state. Preserve the snapshot
through the full retention and audit review period.
