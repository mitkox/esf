# ESF v0.5.0 Helm chart

This chart runs one factory worker and an optional, loopback-bound Machinist
console. CubeSandbox 0.7.2, Temporal 1.32.0, and PostgreSQL 16.15 are
operator-managed dependencies. The chart deliberately has no public console
Service. Use `kubectl port-forward` through an authenticated Kubernetes API
session to reach the console. Set `console.factoryReadClaim` to the factory's
RWO claim and `console.factoryReadTokenSecret` to a separate Secret to enable
read-only factory pages. The console pod is then scheduled on the factory node;
its configuration must set `[factory_read] root =
"/var/lib/esf/factory-read"` and `token_file =
"/etc/esf/factory-read-token"`. Provision the token with at least 32 random
non-whitespace characters. The browser holds it only in session storage.

Supply existing configuration Secrets and qualified image digests in
`values.yaml`. Set `factory.storage.existingClaim` and
`console.storage.existingClaim` to local or block-backed ReadWriteOnce claims,
or set a storage class that provisions them. NFS and RWX-backed SQLite are
unsupported. The startup probe runs `factory doctor --profile production
--offline`; install the pinned agents on the optional agent claim before
starting the worker. The NetworkPolicy denies ingress and permits only DNS plus
the explicit egress CIDRs and ports you supply. Configure the CNI to enforce
NetworkPolicy and include Cube control/data, Temporal, and OTLP destinations.

## Upgrade and recovery

1. Stop new submissions, allow active runs to finish, and verify sandbox
   cleanup and pending quality exports. Scale this chart's workloads to zero.
2. Take consistent snapshots of the factory and console PVCs and the
   operator-managed Temporal/PostgreSQL state. Save configuration, payload
   keys, image digests, and the release inventory with the snapshot.
3. For an existing CubeSandbox Kubernetes installation upgrading to 0.7.2,
   follow the upstream node-drain procedure and acknowledge the host-network
   change with `hostNetworkChangeAck` before admitting ESF work.
4. Verify image digests and release provenance, update the chart values, then
   run `helm upgrade`. Recreate strategy keeps a single authoritative pod; do
   not use rolling or multiple replicas. Check probes, `factory doctor`,
   representative historical runs, and a disposable new run before resuming
   submissions.

Rollback after a schema migration means scaling down, restoring the matching
PVC and Temporal snapshots, and reinstalling the matching earlier images
and chart. A binary downgrade against migrated state is unsupported. Perform a
restore drill in an isolated namespace and storage set before production use.

No Kubernetes acceptance or restore drill is recorded for this chart yet; the
release inventory keeps the deployment qualification gate pending.
