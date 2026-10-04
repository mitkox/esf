# ESF 0.6.0 candidate and production qualification

0.6.0 remains a candidate until every gate below has retained evidence for the
exact final source commit and built assets. Candidate builds do not publish a
release. The deployment model has one authority, local or block-backed SQLite,
and drained upgrades. HA and rolling upgrades are unsupported.

## Immutable inputs and post-build results

`release/inventory.json` is the canonical input inventory. Its embedded copy,
binary defaults, Python root project and lock, image build arguments, and chart
versions must agree. Independently versioned Python packages retain their own
versions. Historical 0.5.0 release records remain historical.

Build the final source twice with `scripts/release.sh v0.6.0-rc.1 OUTPUT` and
compare every byte. `scripts/verify-release.sh OUTPUT v0.6.0-rc.1` checks the
twelve archives, contents, checksums, binary versions, and target metadata.
Run these repository commands in the designated Cube validation environment.
The `Release` workflow can also build an unpublished, attested artifact using
`workflow_dispatch` on the reviewed candidate branch. Linux and macOS CI remain
required. Do not create the final tag or publish as part of candidate review.

Build candidate images from the same source through `Candidate images`. Retain
each image manifest digest and each platform's SBOM, vulnerability assessment,
license review, and provenance. Create the Cube template from its image digest;
retain the READY template identity, snapshot hash, recipe hash, DNS configuration,
and verified harness executable hashes.

Post-build results belong in `qualification-manifest.json`, alongside sanitized
files under `evidence/`. An image cannot embed its own digest. Required fields:

| Field | Required value |
| --- | --- |
| `schema` | `esf-qualification/v1` |
| `release`, `commit` | `v0.6.0` and exact Git HEAD |
| `inventory_sha256` | SHA-256 of the canonical input inventory |
| `artifacts` | Filename to SHA-256 map for all twelve archives, release manifest, inventory, archive SBOM, and checksums |
| `evidence` | Relative `evidence/...` filename to SHA-256 map; regular files, no symlinks or traversal |
| `gates` | All required gates with `status: passed` and nonempty `evidence` references into the verified map |
| `images` | Each inventory image name with immutable `digest`, per-platform `sboms` references, and `platforms` mapping each platform to its manifest `digest` and `security_evidence` references |
| `template` | Nonempty `id`, snapshot `sha256`, and matching `recipe_sha256` |
| `transitive_licenses` | `reviewed`, supported by retained notices and review |

Required gates are `vm`, `kubernetes`, `soak_24h`, `restore`, `security`,
`performance`, `temporal_replay`, `agent_acceptance`, `cube_lifecycle`, and
`migration_drill`. Missing, pending, stale, malformed, or mismatched evidence
fails qualification. The verifier validates bindings and quantitative limits;
reviewers must assess the evidence itself. An attestation authenticates the
workflow and bytes, and does not replace that assessment.

`soak_24h.targets.vm` and `.kubernetes` each record `duration_seconds >= 86400`,
`unexpected_failures: 0`, and `unreconciled_sandboxes: 0`. Performance targets
each contain keys `1`, `4`, and `8`, with at least 30 `batches`, zero failures and
leaks, and positive baseline/candidate values for `p95_duration_seconds` and
`peak_worker_rss_bytes`. Candidate values must be at most 120% of the matching
0.5.0 baseline. Use the same deterministic fixture and equivalent configurations;
retain raw measurements privately and publish sanitized aggregates.

```sh
python3 scripts/verify-release-inventory.py --require-qualified \
  --qualification-manifest qualification/qualification-manifest.json \
  --artifact-dir candidate-assets
```

Upload the sanitized bundle as `esf-qualification-evidence`, then dispatch
`Attest production qualification` on the exact candidate commit with the
candidate and evidence run IDs. It verifies the candidate provenance and all
bindings before attesting the companion manifest. Final promotion requires
`ESF_PROMOTION_RUN_ID` and `ESF_QUALIFICATION_RUN_ID`, the same source digest,
and the qualification workflow's attestation. Promotion preserves the qualified
RC archive bytes and retains its evidence bundle.

## Acceptance evidence

Retain production doctor, permissions, actual CNI enforcement, probes, service
restart, shutdown, persistence, backup/restore, upgrade, and rollback results for
both VM and a dedicated Kubernetes namespace. Configurations, task queues,
storage, credentials, and namespaces must be separate from existing workloads.

Exercise verified success, agent and verification failure, rejected admission,
cancellation, timeout, interrupted worker, failed cleanup, review approval,
rejection and timeout, and rework lineage. Check expected verdicts and artifacts,
egress and metadata blocking, gate tampering, output limits, encrypted-history
leakage, Cube suspend/resume, and zero unowned or unreconciled sandboxes.

Conformance proves infrastructure behavior. Model acceptance needs the pinned
OpenCode and Unreal harnesses against real compatible endpoints. Unreal 0.2.0
requires `/responses`; a provider offering only chat completions is insufficient.
An authorized local HTTP endpoint is suitable for a scoped development check
when reachable, while production doctor still requires production credential
transport. Never include credential values, private configs, raw histories,
task contents, or host identifiers in published evidence.

## Drained 0.5.0 upgrade and rollback

Stop admissions and drain active workflows before changing binaries. Preserve
the 0.5.0 configuration and schema versions; snapshot the console SQLite state,
factory artifacts and indexes, Temporal/PostgreSQL state, payload keyring,
credentials, template identity, image digests, and patch hashes consistently.
Replay retained 0.5.0 histories with the candidate and verify old manifest and
artifact reads. Test new work, then restore the matching baseline snapshots and
repeat the read/replay and disposable-run checks before claiming rollback.
Codec round trips alone do not prove history replay or a deployment restore.

QMS is removed. Obsolete `[qms]` configuration is rejected by strict decoding.
Remove that block after backing up the old configuration; remove obsolete
QMS-specific secret references and integrations. Keep the retained Temporal
version marker and historical workflow names for replay. Do not restore QMS
features or rewrite historical evidence. No state schema change is intended
by the version bump; the drained migration drill must prove that compatibility.
