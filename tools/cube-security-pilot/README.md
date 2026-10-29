The pilot command is trusted host orchestration. It builds no application and
executes no target or OpenCyber code on the host. It stages exact synthetic
candidate bytes in a disposable Cube VM, calls `ExportCandidate`, and passes the
retained archive to the same `CubeExecutor.Assess` used by the factory activity.

Build the two trusted entrypoints on the host:

```sh
rtk proxy env CGO_ENABLED=0 go build -o /tmp/security-assessment ./tools/security-assessment
rtk proxy env CGO_ENABLED=0 go build -o /tmp/cube-security-pilot ./tools/cube-security-pilot
rtk proxy sha256sum /tmp/security-assessment
```

After the endpoint-only route qualification passes, use its receipt and the
native runtime qualification receipt. Supply the independently held driver hash:

```sh
rtk proxy /tmp/cube-security-pilot \
  --config /absolute/path/to/operator/factory.toml \
  --runtime-qualification /absolute/path/to/private/runtime-qualification.json \
  --route-proof /absolute/path/to/private/route-isolation.json \
  --driver-path /tmp/security-assessment --driver-sha256 '<sha256>' \
  --guard-socket '<operator guard socket>' --ca-file '<public preview CA>' \
  --report /absolute/path/to/private/pilot-three-pairs.json \
  --repair --repeat 3
```

`--repair` explicitly requests a separate synthetic source replacement: one
known owner lookup receives the `owner != user` guard. Each repaired run has a
new candidate/artifact/request identity and a parent run ID. An assessment never
repairs its own target. Input fixture bytes must match the route-qualified
fixture hash; the substitution also requires exactly one known vulnerable
lookup. Neither target responses nor model output can propose a replacement.

The runtime profile retains the OCI index digest as build provenance and pins
the local Cube root filesystem artifact separately. Before allocating assessment
VMs, the executor reads bounded control-plane template metadata and requires
the exact READY template plus either its matching immutable OCI image reference
or the matching `cube-rootfs-sha256` annotation on a single container. An
omitted or mismatched local artifact hash cannot use the OCI build receipt as
an implicit fallback.

The assessor driver receives the controller's numeric endpoint address through
`--endpoint-address`. Its embedded isolated Python launcher resolves only the
exact HTTPS hostname and port from the private profile to that numeric address.
It preserves the hostname for TLS verification and does not modify `/etc/hosts`.
Other DNS destinations fail before the original numeric-only resolver runs.

Use a fresh report path for each fault observation:

```sh
rtk proxy /tmp/cube-security-pilot <same trusted flags> \
  --report /absolute/path/to/private/pilot-startup.json \
  --scenario startup-failure --repair --recovery
rtk proxy /tmp/cube-security-pilot <same trusted flags> \
  --report /absolute/path/to/private/pilot-cancel.json \
  --scenario cancel --fault-after 1500ms --repair --recovery
rtk proxy /tmp/cube-security-pilot <same trusted flags> \
  --report /absolute/path/to/private/pilot-timeout.json \
  --scenario timeout --fault-after 1500ms --repair --recovery
```

Startup failure stages a separate explicit `SystemExit` candidate. Cancellation
and timeout inject a context failure after the chosen elapsed delay from the
assessment invocation; that delay alone does not establish which runtime phase
was interrupted. Fault classification requires the matching typed context cause
captured before detached cleanup; a timer firing during cleanup cannot mask an
earlier unrelated failure. A recovery is a fresh child assessment, explicitly requested
by `--recovery`, and starts only after the failure and complete cleanup are
observed. Unknown allocation acknowledgements remain incomplete and stop the
pilot; active assessment requests are never retried.

The receipt and its `.private` directory are created fresh with private file
permissions. Candidate archives, inventories, exact selected evidence,
manifests, and ownership journals remain there. The receipt records all four
assertions, external evidence verification, candidate lineage, request counts,
elapsed times, cleanup, and unavailable memory measurements. Cube API
credentials stay in the host process. The public preview CA is added to this
orchestrator's process trust through `SSL_CERT_FILE` for the provider's bounded
reader; no certificate verification is disabled.

Exit zero means the specifically requested synthetic behavior and cleanup were
observed. `decision` remains `incomplete` and `integration_enabled` remains
false: this command alone cannot qualify the full lifecycle failure matrix,
controller restart recovery, representative delivery economics, independent
review, human approval, configured AI inference, or production acceptance.
