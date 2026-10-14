# Local Cube callback routing

The ESF development stack uses CubeSandbox 0.7.3. Route-aware egress lets
CubeVS use the host routing table for local destinations. Set
`cube_router_enable = true` in Cubelet's network plugin configuration after
preserving a backup and confirming there are no active sandboxes. Restart
only Cubelet and confirm create/execute/destroy smoke before accepting the
change. Restore the backup and restart Cubelet if connectivity regresses.

Host-local callbacks use a separate path from public egress: on the validated
deployment, packet headers showed `cube-dev` ingress with allocated VM source
addresses in `172.31.64.0/18`. The callback fixture also needs a host firewall
permission. The tested rule is limited to that interface and source range,
destination host `192.168.0.16`, and TCP port `18080`:

```sh
sudo ufw allow in on cube-dev from 172.31.64.0/18 to 192.168.0.16 port 18080 proto tcp comment 'ESF Cube callback'
```

This is a development fixture permission, requiring the operator's explicit
firewall authorization. It does not permit other host ports or replace Cube's
per-sandbox destination allowlist. Preserve UFW rule backups before applying
it; remove it with the corresponding `ufw delete allow ...` command if the
callback fixture is no longer required. The recorded validation used a
temporary rule; persistence remained pending approval at delivery time.

Load the local `.env` values before running smoke or network probes. The probe
also needs `CUBE_PROBE_HOST_IP=192.168.0.16` and
`FACTORY_CUBE_VERSION=0.7.3` (or `-cube-version 0.7.3`) so its create request
uses the deployed API's denial field. Factory runs read `sandbox.version`
from `factory.toml` for the same compatibility decision.

Require all three probe scenarios to pass: host access blocked without an
allowlist, allowed with an explicit host entry, and still allowed with public
internet denied while direct public HTTPS is blocked. The public check
resolves on the driver and retains TLS verification; blocked sandbox DNS is
not accepted as evidence of internet denial. After each run, verify no leaked
sandboxes. See the [0.6.1 upgrade record](../release/qualification/component-upgrade-2026-10-06.md)
for exact local artifacts, test results and qualification boundaries.

## CubeEgress marks on a host with Calico or Tailscale

Cube 0.7.3 selects HTTP and HTTPS interception using packet marks. Its default
marks `0xCE010000` and `0xCE020000`, masked by `0xFFFF0000`, overlap the
`0x000F0000` bits that Calico clears in its raw PREROUTING chain. Both defaults
become `0xCE000000`, so the TPROXY rules no longer match. A running proxy and
healthy admin endpoint alone do not establish working credential injection.

Cube supports an operator-owned `/etc/cubeegress/l7-marks.conf`. On this local
deployment, reserve the top byte for Cube and leave Calico's `0x000F0000` and
Tailscale's `0x00FF0000` bits available:

```sh
CUBE_L7_MARK_HTTP=0xCE000000
CUBE_L7_MARK_HTTPS=0xCF000000
CUBE_L7_MARK_MASK=0xFF000000
```

Confirm the chosen bits are free on the actual host before applying them.
With no active VMs, stop Cube's egress network unit **before** replacing the
configuration so its teardown reads the old marks. Preserve the previous
configuration, restart Cubelet to reload the eBPF programs, and start the
egress network and proxy units. Check matching `ip rule` selectors, distinct
HTTP/HTTPS TPROXY counters, an allowed provider request, and denied paths.
Keep the previous configuration until those checks pass. Change only Cube's
configuration; retain the existing Calico and Tailscale rules.
