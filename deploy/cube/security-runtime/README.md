# Native assessment runtime packaging

The runtime binds OpenCyber source, resolved Python wheels, installed package
versions and the actual derived OCI image. It supports the selected native
HTTP/API, supplied-log and evidence profile. The final qualification receipt is
separate from route and factory-integration acceptance.

`runtime.lock.json`, `requirements.lock` and `wheels.sha256` identify the actual
userspace installed in the Cube builder. The source is revision
`09692d89e3cb9dce3506cd4e6fc92c9e4c3c16b6`; the archived source SHA-256 is checked
before extraction. Python is 3.13.16, MCP 2.3.0, Playwright 1.64.1 and requests
2.34.2. Chromium and the external executable catalog are not installed.

## Build boundary

Run `build-runtime.sh` and `runtime_inventory.py` **inside the owned Cube
builder**, using its pinned Python image, retained builder wheel directory and the exact source archive at
`/tmp/opencyber-source.tar.gz`. The script resolves wheels into a private
artifact directory, then installs exclusively from those wheels. Retain their
hashes and frozen versions. Changed resolution or rebuilt wheel bytes require
fresh image identity and requalification; a source version string alone cannot
reuse an earlier runtime receipt.

The inventory generator refuses to run after the wheel directory has been
omitted from a packaged runtime. Preserve the builder's generated inventory;
do not replace it with a new inventory containing an empty wheel map.

Export only `venv/`, `source/`, `runtime.json`, `requirements.lock` and
`wheels.sha256` from `/opt/security-runtime/` as an owned payload. Keep package
wheels and raw evidence in private factory artifact storage. Do not export
engagements, credentials, host configuration or temporary probe state into the
image.

The factory host may unpack that payload as data into a fresh build context
with this layout:

```text
envd
entrypoint.sh
runtime/
```

Use the pinned envd binary with SHA-256
`58a75cbde0fd1bb79befe77589ad34e49a0d6795ea5e36bba23ffaf93192caef`.
`Dockerfile.security` performs COPY and image-metadata steps only; it executes
no project, package-installation or assessment code on the host. Package the
image in the local private registry, import it by immutable digest, wait for
READY and record the actual platform image/rootfs digest. Leave the normal
factory template configuration unchanged.

## Qualification result

The derived image's OCI index is
`sha256:be24e270c130f3c99ac5989828bd85e98a8bdc07d3991f15d744ad412517b4ed`;
its amd64 image manifest is
`sha256:44da6b7bc4ed70ece63f1b7218f41869eb03fbf5d5d2595ecde62b324a1e136d`.
The READY rootfs digest is
`d603978574994277a16d3f00be0ac73db53f2b79d3122e194985b42a21f80f12`.
Local template IDs and private receipts stay in the operator's evidence.

The public recipe omits a private source-location label from the historical
qualified image. The recorded digest identifies that retained image; rebuilding
the sanitized recipe produces a new image identity and requires new pins and
qualification. Source/archive identities remain explicit in the lock file.

From a fresh VM under deny-all egress, `probe_runtime.py` verified real stdio
initialization, required tool contracts, compact catalog alignment, engagement
creation, supplied-log coverage, evidence export/verification and audit-chain
verification. Recorded full catalog digest is
`6ff0d628d1e93da78345b05ad63cdcd27adade55de29938ead97c4a7560b6660`;
the advertised compact subset digest is
`1c50da349bf4e7e162e34f3554840d7190926ddd559c7b0f23628ea91bb92837`.
The compact profile advertises other native tools too; discovery mode is not
the capability boundary. The trusted factory adapter must still constrain
actual tool names, inputs, paths, destinations and budgets.

The selected evidence bundle was copied outside Cube before teardown. Its
independently recomputed hash matched the factory-held expected SHA-256
`6b46d2b3d15cc0511d4c87c2f44981515043094e8f272228412e444ecf37c396`.
MCP startup plus selected operations took 1.55 seconds in the observed VM;
the installed venv occupied 204 MiB, and the image's compressed rootfs layers
were approximately 119 MB. These are single-run observations, not performance
guarantees.

The full-toolkit self-test remains degraded with 164 missing external
executables. It reports native catalog subset alignment correctly. No HTTP
request against an application target was made for this template-only result;
that depends on the separately qualified route. VM cleanup was verified for
both builder and fresh-template probe. This does not claim the full lifecycle
matrix, human approval, production integration or distribution clearance.

The first Alpine base attempt reached READY but could not execute SDK commands
because `/usr/bin/nice` was absent. Its owned VM and template were removed. The
selected Debian slim Python base satisfies that actual envd prerequisite.
