# ESF v0.5.0 release procedure

The release inventory is the qualification record. `scripts/verify-release-inventory.py
--require-qualified` refuses RC and final archives while VM, Kubernetes,
security, restore, 24-hour soak, template identity, image digests, or license
review are pending. Update the inventory only with evidence from the target
versions and rerun the relevant matrix when a pin changes.
After each inventory edit, copy it to `internal/releaseinfo/inventory.json` so
`factory version --json` reports the same record; the verifier enforces an
exact match.

1. Preserve local intake work, build from a clean tree based on `399504f`, and
   confirm the Machinist integration through `3943516`. Run `make lint`, Go
   race tests/vet, frontend tests/build, optional Python tests, migration and
   Temporal replay fixtures. Verify OpenCode and Unreal executable hashes.
2. Build candidate factory, console, managed-worker, and optional intake
   images through `Candidate images`. Copy their immutable digests to the
   inventory after VM and Kubernetes qualification. Keep the console on
   loopback and use a read-only factory token. Check Cube 0.7.2's node-drain
   and `hostNetworkChangeAck` procedure for existing Kubernetes clusters.
3. Complete the deployment matrix, failure/security scenarios, size and
   concurrency benchmarks, 24-hour soak, and isolated backup/restore drills.
   Record no high or critical exploitable shipped findings and no unreconciled
   sandboxes or duplicate agent executions. Mark a gate `passed` only after
   retaining its test record.
4. Create an annotated `v0.5.0-rc.N` tag on the qualified commit. The release
   workflow builds reproducible archives, checks the 25% factory size gate,
   publishes the CycloneDX dependency graph, and attests provenance and SBOMs.
   Verify the published archives and both deployment profiles before promotion.
5. Set repository variable `ESF_PROMOTION_SOURCE` to that exact qualified RC
   tag, then create annotated `v0.5.0` on the same commit. The final workflow
   checks the RC release, its checksums and attestations, copies the archive
   bytes under final asset names, and attests those same bytes for the final
   tag. It does not rebuild binaries. Image deployments continue to use the
   qualified digests from the inventory.

The RC and final archives embed the canonical `v0.5.0` binary version; the
release manifest records the RC or final distribution tag. Monetary budgets
remain advisory where providers do not expose an enforceable limit. Rollback
after any schema upgrade restores the matching snapshot before the earlier
binary starts. Automatic rolling upgrades and multiple authoritative ESF
instances are unsupported in this release.
