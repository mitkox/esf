# Production readiness review

Reviewed on 2026-09-19. This review improves the implementation; it does not
certify a deployment. The bundled Temporal stack and host-specific Cube settings
are development configuration.

## Corrections made

| Finding | Resulting behavior |
| --- | --- |
| Agent activities configured a three-minute heartbeat timeout without sending heartbeats | Agent execution sends an initial heartbeat and repeats every 30 seconds, stopping when the activity ends or is cancelled. |
| Patch extraction or collection could fail while the factory reported success | Missing patch evidence prevents `SUCCEEDED`; an empty, successfully collected patch remains valid. |
| A leaked sandbox could accompany a successful factory verdict | Successful work with unverified cleanup is reported as `INFRASTRUCTURE_FAILED`; cleanup details remain in the manifest. |
| Cancellation outside the agent stage was misclassified | Cancellation is recognized across workflow stages; a cancelled agent retains its `CANCELLED` outcome. |
| Redaction operated on encoded JSON | String values are redacted before re-encoding, preserving valid JSON, escaped-secret matching, and integer precision. |
| The fallback patch reference was added after the manifest was saved | The patch is saved and referenced before publishing the manifest. |
| Artifact replacement truncated files in place | Replacement uses a temporary file, file sync, atomic rename, and parent-directory sync. |
| Blocked output could race the command timeout | Deadline-triggered output errors preserve the timed-out verdict. |

Regression coverage includes both patch-collection phases, cleanup failure,
cancellation at four stages, agent heartbeat emission, escaped-secret JSON,
fallback patch references, and concurrent artifact readers. The existing blocked
output timeout/cancellation tests also passed 30 consecutive repetitions.

## Local validation

- `go test -race ./...`
- `go vet ./...`
- Builds of `./cmd/factory` and `./cmd/machinist`
- Frontend `npm test` and `npm run build`; generated assets match the tracked files
- Locked frontend dependency installation reported zero npm audit vulnerabilities

Go checks used Go 1.26.6. Frontend checks used Node 24.19.0; CI remains the check
for its configured Node version and macOS. No live infrastructure was changed.
A Go dependency vulnerability scan was also completed (see below).

## Additional production hardening

- Remote Temporal connections now require verified TLS, with optional mTLS or
  API-key authentication. Incomplete certificates, missing keys and invalid CAs
  fail configuration validation.
- Optional Temporal payload encryption protects prompts, activity results and
  failure messages using AES-256-GCM. The versioned keyring supports rotation
  while retaining old keys for replay.
- Agent and verification evidence errors are returned as outcomes, preventing
  success without automatically rerunning completed work. Repository status and
  cleanup evidence writes now report failures too.
- Sandbox metadata includes the workflow execution ID. A lost create response
  can be recovered on retry; an unresolved create is never blindly repeated.
  Cleanup searches matching metadata even when creation returned no sandbox ID.
- Configured agent, verification and total execution timeouts are enforced.
  Cleanup and finalization run independently after the execution timer expires.
- Workers honor context cancellation and SIGTERM and have a bounded shutdown
  grace period. The package-level mutable activity-attempt hook was removed.
- A hardened systemd worker unit and a deployment/credential-rotation/recovery
  guide are included. CI builds both factory and Machinist binaries and runs
  a pinned Go vulnerability checker.
- The Unreal harness supports CubeEgress credential injection. The provider key
  is resolved only inside the worker activity, never serialized through
  Temporal, and never exposed to the microVM. Runtime egress changes to
  deny-by-default before the agent starts.

## Live validation performed

The existing local services were used without changing their configuration:

- Real Temporal: a 190-second blocked agent survived the three-minute heartbeat
  deadline, completed successfully and verified cleanup.
- Real Temporal: workflow completion, cancellation, timeout cleanup, encrypted
  payload processing and raw-history inspection for plaintext prompt leakage.
- Real Cube: disposable sandbox command execution, separate stdout/stderr and
  exit status, file round trip, ownership metadata round trip and idempotent
  destruction. Test sandboxes were destroyed by the tests.
- Go vulnerability analysis using `govulncheck` v1.8.0: no vulnerabilities found.

Fault-injection tests cover missing audit writes, lost create responses,
execution ownership, total-timeout cleanup, ciphertext tampering, missing keys,
key rotation and encrypted failure messages.

## Deployment requirements

Follow [production deployment](production-deployment.md) to configure the service,
TLS, encryption and key management. These are operator choices; this review did
not install a service, replace credentials, or expose any endpoint.

Use durable storage shared by all workers that may handle a run. Keep backups of
both Temporal and artifacts, and retain decryption keys for the full history and
backup lifetime. The deployment still needs an isolated restore drill and a load
check at its intended concurrency; neither can be inferred from unit tests.

Drain active runs before upgrading from the previous workflow implementation.
Rolling-upgrade replay compatibility against existing production histories has
not been tested. When provider creation is ambiguous and a sandbox is not yet
visible, the factory fails conservatively and attempts metadata cleanup; finite
provider TTLs and operator reconciliation remain the final resource backstop.
