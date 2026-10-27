# OpenCyber assessment qualification

[Issue #50](https://github.com/mitkox/esf/issues/50) requires a pinned runtime
and a route limited to one disposable application endpoint. The original
shared proxy IP allowance failed because other routed ports remained reachable.
The replacement uses an exclusive owned address, a protocol/port firewall
guard, and a factory-side HTTPS broker bound to one target instance and port.
See the [qualification report](../../release/qualification/opencyber-cube-pilot.md)
for observed results and remaining gates.

## Selected runtime

The [runtime build guide](security-runtime/README.md) describes pinned
Python/OpenCyber userspace, Cube-only installation, COPY-only OCI packaging,
and template import. Retain source, wheel, image, root-filesystem, inventory
and catalog identities together. A READY template does not prove tools work.
The selected native HTTP, supplied-log and evidence profile excludes external
scanners, browser engines and hardware.

## Endpoint qualification

Build trusted orchestration on the host. Application and assessment code run
inside separate Cube microVMs.

```sh
rtk proxy env GOCACHE=/tmp/esf-build-cache go build -o bin/cube-security-route-guard ./tools/cube-security-route-guard
rtk proxy env GOCACHE=/tmp/esf-build-cache go build -o bin/cube-security-qualify ./tools/cube-security-qualify
rtk proxy sha256sum bin/cube-security-route-guard bin/cube-security-qualify
```

The operator starts the reviewed, frozen root helper in another terminal:

```sh
rtk proxy sudo /absolute/path/to/cube-security-route-guard \
  --socket /tmp/esf-issue50-guard-0123456789abcdef/control.sock --timeout 45m
```

It creates its own root-owned socket directory, authenticates caller UID, and
accepts only owned guard operations on exclusive TEST-NET addresses and fixed
port 9445. No Cube credential or caller-supplied shell command enters it.
Normal shutdown, SIGTERM and lease expiry clean its owned guards. SIGKILL
still requires independent kernel-state reconciliation: the root service has
no persistent restart journal.

After `ready`, run the unprivileged controller:

```sh
rtk proxy bin/cube-security-qualify \
  --config /absolute/path/to/local/factory.toml \
  --fixture testdata/security-assessment/tenant-api/server.py \
  --assessment-template '<pinned READY template ID>' \
  --ca-file /absolute/path/to/public-preview-ca.crt \
  --guard-socket /tmp/esf-issue50-guard-0123456789abcdef/control.sock \
  --report /absolute/path/to/private/new-route-receipt.json
```

The public preview CA changes trust only; private keys are rejected and TLS
hostname verification stays enabled. The broker issues an ephemeral endpoint
certificate, checks a per-attempt capability, permits fixed GET paths, injects
the target preview capability only on the host, bounds responses and refuses
redirects. No factory management credential enters either VM.

Only the assessor's dedicated endpoint address is allowed out. The guard
accepts TCP 9445 and rejects other ports/protocols. Reachable TCP and UDP echo
controls precede removal of their temporary exceptions. UDP denial requires
no echo AND an increment of the exact owned reject counter; missing ICMP
feedback alone proves neither success nor failure. Positive HTTPS requests
bracket authority, path, method, authorization, redirect and direct-destination
checks. Target-origin checks have same-tuple reachable factory controls. A
target-local write must also leave the assessor's externally hashed sentinel
unchanged. This covers tested network paths and separate filesystems, not a
general hostile-kernel isolation proof.

`route_qualified=true` is a route result only. Overall `decision=incomplete`
and `integration_enabled=false` remain until the independent runtime, lifecycle,
workflow and application gates have their own acceptance evidence.

## Evidence and recovery

Keep fresh private receipts and adjacent `.probe.json` evidence together.
Receipts bind binary/probe/fixture/CA hashes and exact resource ownership. Git
HEAD does not identify dirty candidate bytes; retain transferred snapshots.
Ownership is persisted before each allocation. Cleanup attempts all owned VM
deletions and verifies inventory before removing the owned address/guard.
Unrelated resources stay untouched. Lost create acknowledgments remain
incomplete even after an empty immediate inventory; allocation is not retried.

Recover an interrupted controller using its existing receipt and guard service:

```sh
rtk proxy bin/cube-security-qualify --config /absolute/path/to/local/factory.toml \
  --report /absolute/path/to/private/existing-route-receipt.json --cleanup-only
```

The [pilot driver](../../tools/cube-security-pilot/README.md) exercises actual
bounded MCP assessments, explicit replacement candidates and failure cases.
The [factory guide](../../docs/security-assessment.md) documents the optional,
scoped advisory stage. Keep activation disabled until prerequisites are accepted.
Synthetic results do not grant human approval or production qualification.
