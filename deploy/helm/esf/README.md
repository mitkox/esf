# ESF v1.0.0-rc.1 Helm chart

This chart runs the factory appliance: one factory worker Deployment, one
execution API Deployment and Service, and one connector intake Deployment. The
worker is the only Temporal poller; the connector serves authenticated forge
webhooks and reconciliation and executes no run steps. CubeSandbox 0.7.3,
Temporal 1.32.2, and PostgreSQL 18.6 are operator-managed dependencies. The
factory CLI remains the operator control plane; the API is the versioned `/v1`
integration surface. If the operator runs Temporal's UI, it provides
workflow-state visibility; it does not replace the console's evidence view.

Supply existing configuration Secrets and qualified image digests in
`values.yaml`. Set `factory.storage.existingClaim` to a local or block-backed
ReadWriteOnce claim, or set a storage class that provisions one. NFS and
RWX-backed storage are unsupported. All workloads mount that claim, so the chart co-locates the API and connector
pods with the factory worker and requires `factory.enabled=true`; use storage
that permits the co-located mounts. The API and worker startup and readiness
probes run `factory doctor --profile production --offline
--skip-connector-health`, a configuration-only check of the API listener when
enabled, configured connectors, token-file permissions, and the evidence
signing key. A first install, a connector outage, or a sweep gap longer than the
freshness window therefore does not remove run execution from service. The
connector Deployment's own startup and readiness probes keep the full
production profile, including the durable connector-health snapshot (each
connector's status, reason and freshness), so connector health gates the
connector workload and the pod becomes ready only after every configured
connector has reported. Operator pre-flight `factory doctor --profile
production` always keeps the connector-health check. Install the pinned agents
on the optional agent claim before starting the worker. Each
Deployment uses one replica, the `Recreate` strategy, the restricted non-root
security context, a read-only root filesystem, no added capabilities, and
`automountServiceAccountToken: false`. The NetworkPolicy denies all ingress
except the API Service port and permits only DNS plus the explicit egress CIDRs
and ports you supply. Configure the CNI to enforce NetworkPolicy and include
Cube control/data, Temporal, OTLP, and forge destinations.

The API Deployment exposes `api.port` through a ClusterIP Service. Set the
`[api].listen` address in `factory.toml` to bind that port. A non-loopback
listener requires native TLS: create a Secret with `tls.crt` and `tls.key`, set
`api.tlsSecret`, and point the configuration at `/etc/esf/tls/tls.crt` and
`/etc/esf/tls/tls.key`. A loopback-only listener cannot be reached through the
Service and needs a TLS-terminating proxy. The API token must be at least 32
bearer-alphabet characters.

The worker Deployment hosts all workflow and activity types, including forge
adapter publication activities. The connector Deployment is not a second
Temporal worker: enable it whenever `[connectors.*]` is configured. Keep
`factory.enabled=true`; the chart requires the worker when the API or connector
is enabled because all workloads share its ReadWriteOnce claim. Set
`[connectors.*]` and `[pipelines.*]` in the configuration and place each
connector token in `factory.credentialSecret`.

`factory.credentialSecret` is mounted only in a restricted init container. The
factory image copies projected Secret files into a memory-backed volume as
regular files with mode `0600`, inside an owner-only directory. The worker and
API mount that directory read-only at `/etc/esf/credentials`; credential paths
keep their existing names. Include the API token, connector tokens, and the
PKCS#8 ed25519 evidence signing key in that Secret and reference them from
`factory.toml`. Staging rejects escaping symlinks, nonregular files, files over
64 KiB, and totals over 1 MiB. Drain and recreate the pod when rotating
credentials; running pods retain their staged credential snapshot.

## Upgrade and recovery

1. Stop new submissions, allow active runs to finish, and verify sandbox
   cleanup. Scale the worker, API, and connector Deployments to zero.
2. Take consistent snapshots of the factory PVC (and, when upgrading from a
   Machinist release you want to preserve, the console PVC) and the
   operator-managed Temporal/PostgreSQL state. Save configuration, payload
   keys, API and connector tokens, TLS private keys, the evidence signing key,
   image digests, and the release inventory with the snapshot.
3. For an existing CubeSandbox Kubernetes installation upgrading from a CubeSandbox version before 0.7.2,
   follow the upstream node-drain procedure and acknowledge the host-network
   change with `hostNetworkChangeAck` before admitting ESF work.
4. Verify image digests and release provenance, update the chart values, then
   run `helm upgrade`. Recreate strategy keeps a single authoritative pod per
   workload; do not use rolling or multiple replicas. Check probes,
   `factory doctor --profile production`, the API health path, connector
   configuration and health, representative historical runs, and a disposable
   new run before resuming submissions.

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

This chart and its rendered manifests are not Kubernetes/Helm qualification.
Production qualification needs the matching attested companion manifest,
including Kubernetes install/upgrade/rollback acceptance, restore, performance,
and 24-hour soak evidence. See
[the release procedure](../../../docs/release-v1.0.0-rc.1.md) and the open GA
gates there.
