# v0.5.0 host release verification, 2026-09-27

This record covers the HP Z2 development host. It does not qualify the VM or
MicroK8s deployment for production. The required load matrix is 1, 4, and 8
concurrent runs on each target, followed by a 24-hour soak.

## Exact release installed

- The published `v0.5.0` combined Linux/amd64 archive, checksums, release
  manifest, and dependency inventory were downloaded from the GitHub release.
  All downloaded files matched `checksums.txt`; the inventory matched
  `release/inventory.json`, and the archive contained only the five expected
  files.
- The archive's manifest names commit
  `e23f422f9eda5de5b7118d687fb5d9c37deee50e`, promoted from
  `v0.5.0-rc.3`. The extracted `factory version --json` reports that commit,
  v0.5.0, and `temporal_replay=passed`.
- The installed `factory` and `machinist` symlinks now point to
  `/home/mitko/.local/opt/esf/v0.5.0-e23f422`. The previous versioned
  directory remains available for rollback. No factory or Machinist worker
  process was running when the symlinks changed.
- GitHub attestation verification was not rerun on the host: this host's
  `gh` version lacks the `attestation` subcommand. Archive checksums and
  the release manifest were verified.

## Checks on the exact release source and binary

- `go test -race ./...`, `go vet ./...`, and builds of both commands passed
  with Go 1.27.1 on the release commit. The tests needed local socket access
  and a writable temporary Go cache in the tool sandbox.
- The installed release binary validated the existing local-model config and
  verified the pinned OpenCode V2 and Unreal executable hashes inside the
  READY Cube template.
- The installed release binary reached Cube and Temporal, but
  `doctor --profile production` failed on the existing development config
  because it uses plaintext Temporal and non-CubeEgress credentials. A
  separate OpenCode Go test config uses CubeEgress, but the documented HTTPS
  L7 data-path failure remains unresolved.
- MicroK8s was running and its system pods were Ready. No ESF chart was
  deployed in this check.
- Syft 1.42.3, downloaded with its published checksum, scanned the locally
  built managed-worker image as Linux/amd64. Its CycloneDX 1.6 inventory
  included Git, libc6, and OpenSSH. This confirms why the source dependency
  SBOM was insufficient for image review; the local image is not a published
  qualified candidate.

## Qualification credentials prepared

An owner-only qualification CA, 90-day Temporal server/client certificates,
and a random 256-bit payload keyring were generated outside Git under
`/home/mitko/.local/share/esf/credentials/qualification-temporal`. The
directory is mode 0700 and its files are mode 0600. OpenSSL verified both
certificates against the CA. The server certificate is now used by a
separate Temporal 1.32.0 stack with its own PostgreSQL 16.15 volume, bound
to loopback port 7244; the factory client uses the CA and payload keyring.
The server does not require client certificates, so this is verified TLS
validation, not mTLS qualification. The existing owner-only OpenCode Go
key was left untouched. A valid external provider credential cannot be
minted locally.

## Isolated TLS workflow

- The separate Temporal service returned `SERVING` over TLS 1.3 with a
  certificate verified against the qualification CA. The published factory
  binary passed every live `doctor --profile production` check with an
  owner-only TLS configuration and isolated factory data directory.
- A disposable conformance task, `run-2c9a0d5ab1d5283c8ee239d0`,
  completed as `SUCCEEDED`, produced the expected greeting patch, passed
  build and test gates, and reported verified cleanup. A subsequent sandbox
  listing reported no live sandboxes. The temporary worker was stopped.
- The raw 95-event Temporal history was valid JSON and did not contain the
  task text or patch plaintext. This is a limited leakage check, not a
  full cryptographic review.
- A separate mTLS attempt enabled Temporal's global
  `TEMPORAL_TLS_REQUIRE_CLIENT_AUTH` flag. It rejected clients without a
  certificate, but internal history calls then failed with `tls: bad
  certificate`, and the factory doctor timed out. The setting was rolled
  back; the TLS-only stack and doctor were healthy again. Frontend client
  authentication needs a separately qualified Temporal configuration.

## TLS-backed VM concurrency fixture

The published v0.5.0 binary ran the deterministic fixture at the requested
1/4/8 concurrency levels against the isolated TLS Temporal service and
CubeSandbox 0.7.2. All 13 runs passed build and test gates on one agent
attempt, verified cleanup, and left no live sandbox. Raw results and worker
logs are retained at
`/home/mitko/.local/share/esf/qualification/benchmark-v050-tls-20260927-01`.

| Runs | Batch wall time | P95 run duration | Worker peak RSS |
| ---: | ---: | ---: | ---: |
| 1 | 1.30 s | 1.15 s | 51.7 MB |
| 4 | 1.71 s | 1.42 s | 58.8 MB |
| 8 | 2.21 s | 1.89 s | 67.7 MB |

Each level has only one batch of a deterministic fixture. This does not
qualify model-backed workload latency, the Kubernetes target, failure
behavior under load, or a 24-hour soak.

## Isolated TLS restore exercise

- The TLS qualification stack's Temporal and visibility databases were backed
  up with `pg_dump -Fc`. Factory data, configuration, and the CA, certificate,
  and payload keyring were copied into an owner-only backup directory. All 21
  backup files matched recorded SHA-256 checksums before recovery.
- The databases were restored with `pg_restore --clean --if-exists` into a
  second PostgreSQL volume. A separate Temporal 1.32.0 service was started on
  loopback port 7245 while the source stack stayed on port 7244. The recovered
  service became healthy and the published factory binary passed every
  production-profile doctor check using the recovered configuration.
- The recovered Temporal database served the conformance run's full 95-event
  history over TLS; its final event was workflow completion. The recovered
  factory manifest still reported `SUCCEEDED` and verified cleanup. The
  recovered `changes.patch` SHA-256 matched the source artifact:
  `365194a7478c8a4a35e3f59104b27496b1a3eb1f52166cc27c78bfdae8af2769`.
- Evidence is under
  `/home/mitko/.local/share/esf/qualification/restore-drill-20260927`.
  This covers an isolated TLS qualification stack. It does not exercise a
  production VM backup service, Kubernetes volumes and Secrets, external
  credential re-provisioning, or the operator's disaster-recovery runbook.
  The release inventory's restore gate remains pending.

## Published candidate image inventory and scanner matches

[Candidate image workflow run](https://github.com/mitkox/esf/actions/runs/36327428784)
completed successfully from this qualification branch. It built five images
and uploaded and attested nine platform-specific CycloneDX image SBOMs: amd64
and arm64 for factory, console, managed worker, and intake; amd64 for the Cube
template. These are candidate images built after the v0.5.0 binary release,
not promoted release image digests. The SBOM artifacts are retained in the
workflow run and were downloaded for inspection under
`/home/mitko/.local/share/esf/qualification/candidate-images-20260927/sboms`.

Grype 0.119.0 scanned each candidate SBOM with its 2026-09-26 database. Raw
reports and SHA-256 checksums are preserved alongside the SBOMs under
`/home/mitko/.local/share/esf/qualification/candidate-images-20260927`.
The two architectures reported equal high/critical counts for each
multi-platform component.

| Candidate image | High | Critical |
| --- | ---: | ---: |
| Factory | 0 | 0 |
| Console | 0 | 0 |
| Managed worker | 90 | 29 |
| Intake | 64 | 10 |
| Cube template | 24 | 8 |

The Cube template includes `/usr/bin/envd` with Go 1.25.4 and
`golang.org/x/crypto` 0.49.0; Grype lists fixed versions for its critical
matches. SBOM-based Go matching lacks symbol reachability, so these are
findings for triage rather than exploitability conclusions. The image security
gate remains open. A preliminary license inspection also found unknown license
metadata for seven distinct optional-intake Python packages and many license
expressions in the large Debian-based images; transitive license approval is
still pending.

## Gates remaining

The release inventory correctly leaves VM, Kubernetes, restore, security,
performance, 24-hour soak, agent acceptance, Cube lifecycle, and migration
pending. This host update closes none of those gates. Continue with a
production TLS and client-auth deployment, valid CubeEgress credentials,
the live L7 probe, candidate-image security triage and transitive license
review, production restore and rollback, and the 1/4/8-run and 24-hour
matrices before changing the inventory.
