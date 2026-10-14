# Private service deployment

Create system user/group `esf-turn`; install the reviewed static binary at
`/opt/esf/bin/turn-executor` and pinned Claude executable under
`/opt/esf/claude/2.1.272/claude`. Files must be readable by the service user and
not writable by it. Install the unit, a validated profile JSON and an environment
file owned root:esf-turn mode0640 under `/etc/esf`.

The environment file contains the existing `CUBE_API_URL`, `CUBE_TEMPLATE_ID`, `CUBE_PROXY_NODE_IP`,
`CUBE_PROXY_PORT_HTTP`, `CUBE_PROXY_SCHEME`, `CUBE_SANDBOX_DOMAIN`,
Cube authentication variables required by `internal/sandbox/cube/config.go`,
`ESF_TURN_SERVICE_TOKEN` (random minimum32 characters), and `OPENCODE_GO_API_KEY`.
Use the deployment secret manager or protected credential files; do not place
values in source, Run Command bodies, shell history, or public storage. The
sample profile names secret references only. Replace every template placeholder.

The service listens only on loopback8091. Use a private TLS proxy for remote sf;
never expose the executor to browsers. The state directory must be persistent,
backed up, private, and single-writer. A restart fails interrupted attempts and
verifies cleanup; it never replays a potentially effectful model turn.

For secret-free host qualification use `turn-smoke --template READY_ID` with
Cube configuration. Its JSON receipt includes owned VM identity, binary file
roundtrip, and cleanup verification, and exits nonzero for any failed gate.
The probe requests only the Azure metadata instance endpoint, never a managed
identity token, and fails if any HTTP response is reachable from the guest. Run
it both with the default blocked egress and with `--allow-internet` before
enabling cloud credentials on a host with managed identity.
Then test the fixture profile through the private HTTP protocol before enabling
model credentials. Review `docs/sf-turn-executor.md` for limits and evidence.
