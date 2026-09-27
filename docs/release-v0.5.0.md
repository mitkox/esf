# ESF v0.5.0 release procedure

The release inventory records two different qualifications. The `v0.5.0`
distribution is a **binary preview**: pinned inputs, CI, reproducible archives,
checksums, and attestations qualify its downloadable binaries. An RC is a
prerelease built from a clean, annotated tag on `main`. The final tag promotes
the same archive bytes from a verified RC on the same commit. The final
workflow requires `scripts/verify-release-inventory.py --require-preview`.

VM, Kubernetes, container images, agent credentials, security, restore,
performance, 24-hour soak, and template identity remain **deployment
qualifications**. Transitive-license review also remains pending. These checks
are not certified by the binary preview.
`scripts/verify-release-inventory.py --require-qualified` continues to
reject a production qualification while these are pending. The release notes
and embedded inventory expose this status. Kubernetes deployment qualification
targets MicroK8s 1.36.2. Update the inventory only with evidence from the
target versions and rerun the relevant matrix when a pin changes.
After each inventory edit, copy it to `internal/releaseinfo/inventory.json` so
`factory version --json` reports the same record; the verifier enforces an
exact match.

1. Run `make lint`, Go race tests/vet, frontend tests/build, optional Python
   tests, migration and Temporal replay fixtures on the release commit. Verify
   the pinned OpenCode and Unreal executable hashes. CI must pass on that exact
   commit before tagging.
2. Create an annotated `v0.5.0-rc.N` tag on `main`. The release
   workflow builds reproducible archives, checks the 25% factory size gate,
   publishes the CycloneDX dependency graph, and attests provenance and SBOMs.
   Verify the published archive checksums, SBOM, and attestations. If the
   inventory or release policy changes, make a new commit and RC; never re-tag
   an existing RC.
3. Set repository variable `ESF_PROMOTION_SOURCE` to that exact verified RC
   tag, then create annotated `v0.5.0` on the same commit. The final workflow
   checks the RC release, its checksums and attestations, copies the archive
   bytes under final asset names, and attests those same bytes for the final
   tag. It does not rebuild binaries.
4. Before production deployment, build candidate factory, console,
   managed-worker, optional intake, and Cube template images. The candidate
   workflow scans each pushed image digest for Linux/amd64 and Linux/arm64
   (Cube template: Linux/amd64 only), retains the resulting image SBOMs, and
   attests them separately. The source dependency SBOM attached to the binary
   release is not an image inventory. Record qualified image digests after VM
   and Kubernetes acceptance. Complete the per-platform image and transitive
   license reviews, failure and security matrix, concurrency benchmarks,
   24-hour soak, and isolated backup/restore drills. Keep the console on loopback with a read-only factory
   token. Follow Cube 0.7.2's node-drain and `hostNetworkChangeAck` procedure
   for existing Kubernetes clusters. Mark a deployment gate `passed` only after
   retaining its test record, then rerun `--require-qualified`.

The RC and final archives embed the canonical `v0.5.0` binary version; the
release manifest records the RC or final distribution tag. Monetary budgets
remain advisory where providers do not expose an enforceable limit. Rollback
after any schema upgrade restores the matching snapshot before the earlier
binary starts. Automatic rolling upgrades and multiple authoritative ESF
instances are unsupported in this release.
