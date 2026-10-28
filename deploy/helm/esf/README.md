# ESF v0.7.1 Helm chart

This chart runs one factory worker. CubeSandbox 0.7.3, Temporal 1.32.1, and
PostgreSQL 18.6 are operator-managed dependencies. The chart deliberately has
no public UI or Service: the factory is driven by the `factory` CLI. If the
operator runs Temporal's UI, it provides workflow-state visibility; it does not
replace the removed console's evidence view.

Supply existing configuration Secrets and qualified image digests in
`values.yaml`. Set `factory.storage.existingClaim` to a local or block-backed
ReadWriteOnce claim, or set a storage class that provisions one. NFS and
RWX-backed storage are unsupported. The startup probe runs `factory doctor
--profile production --offline`; install the pinned agents on the optional
agent claim before starting the worker. The NetworkPolicy denies ingress and
permits only DNS plus the explicit egress CIDRs and ports you supply. Configure
the CNI to enforce NetworkPolicy and include Cube control/data, Temporal, and
OTLP destinations.

`factory.credentialSecret` is mounted only in a restricted init container. The
factory image copies projected Secret files into a memory-backed volume as
regular files with mode `0600`, inside an owner-only directory. The worker
mounts that directory read-only at `/etc/esf/credentials`; credential paths keep
their existing names. Staging rejects escaping symlinks, nonregular files,
files over 64 KiB, and totals over 1 MiB. Drain and recreate the pod when
rotating credentials; running pods retain their staged credential snapshot.

## Upgrade and recovery

1. Stop new submissions, allow active runs to finish, and verify sandbox
   cleanup. Scale this chart's workloads to zero.
2. Take consistent snapshots of the factory PVC (and, when upgrading from a
   Machinist release you want to preserve, the console PVC) and the
   operator-managed Temporal/PostgreSQL state. Save configuration, payload
   keys, image digests, and the release inventory with the snapshot.
3. For an existing CubeSandbox Kubernetes installation upgrading from a version before 0.7.2,
   follow the upstream node-drain procedure and acknowledge the host-network
   change with `hostNetworkChangeAck` before admitting ESF work.
4. Verify image digests and release provenance, update the chart values, then
   run `helm upgrade`. Recreate strategy keeps a single authoritative pod; do
   not use rolling or multiple replicas. Check probes, `factory doctor`,
   representative historical runs, and a disposable new run before resuming
   submissions.

### Upgrading from a Machinist deployment

ESF 0.6.2 removed the Machinist CLI, local control plane, web UI/console and
managed worker. Before upgrading a chart from an earlier version:

- remove the `console` section from your values file;
- before upgrading, stop the old console and export/snapshot its PVC (or
  `console.storage.existingClaim`); deleting that PVC is irreversible;
- delete any console Deployment or Secret that your installation manages
  outside this chart; `helm upgrade` removes resources created by earlier
  versions of the chart;
- on a host install, archive or delete the Machinist service user's state
  (normally `/home/machinist/.machinist`); it is not cluster state and the
  factory does not read it.

Rollback after a schema migration means scaling down, restoring the matching
PVC and Temporal snapshots, and reinstalling the matching earlier images
and chart. A binary downgrade against migrated state is unsupported. Perform a
restore drill in an isolated namespace and storage set before production use.

Production qualification needs the matching attested companion manifest,
including Kubernetes acceptance, restore, performance, and 24-hour soak
evidence. See [the release procedure](../../../docs/release-v0.7.1.md).
