# Optional advisory security assessment

Factory security assessment is disabled by default. A run may select an
operator-declared profile using `RunRequest.security_assessment_profile`, and
its scope must explicitly grant that profile through `security_assessments`.
The request cannot supply a target endpoint, runtime template, tools, commands,
credentials, or qualification pins. Declaring profiles does not select one.
There is no automatic endpoint discovery or fallback to an unrestricted route.

The concrete endpoint guard and Cube assessment executor require a Linux
factory host. Other platforms can build the ordinary factory and run its
portable tests; requesting this backend fails before allocating assessment VMs.

The operator supplies absolute paths and SHA-256 pins for two JSON files under
`[security_assessment_profiles.<name>]`. The profile follows
`esf.security-profile/v1` and declares the pinned runtime, permitted tools,
required checks, limits, and optional AI provider/model. The qualification file
has this shape:

```json
{
  "schema": "esf.security-qualification/v1",
  "route_qualified": true,
  "cleanup_verified": true,
  "runtime": { "...": "the complete exact runtime object from the profile" }
}
```

This is a trusted operator receipt, not a probe or an inferred approval. Create
and pin it only after retaining actual endpoint isolation, runtime identity,
and teardown evidence. Its runtime must exactly match the profile. Missing,
changed, malformed, or unqualified files fail configuration validation; a
policy change after admission yields explicit incomplete advice. Keep these
files outside candidate source and restrict write access to the operator.
The placeholder target URL in a declared profile is bound by the trusted
executor to the newly allocated target; no other profile field may change.

`factory run --security-assessment <name>` selects the name. JSON/TOML run
specifications use `security_assessment_profile`. The worker requires both
`--security-executor-config /absolute/path/operator.json` and
`--security-executor-sha256 <sha256>` to activate the concrete Cube executor.
Without them the executor is absent. The strict, bounded operator JSON contains
only `guard_socket`, `driver_binary_path`, `driver_sha256`, `target_entrypoint`,
`private_journal_root`, `preview_ca_file`, and `runtime_inventory_sha256`.
Cube addresses and credentials always come from the factory's actual config.

Local Cube ext4 templates expose a root-filesystem artifact rather than an OCI
`imageInfo` reference. Pin `runtime.assets.cube-rootfs-sha256` from the retained
build/import receipt. Before allocation, the executor verifies exact READY
template identity and either the immutable OCI reference or the sole container's
exact artifact digest. It then verifies the inventory inside the assessor.
The image index remains build provenance; it is not inferred from a rootfs hash.

The assessor receives a fixed resolver mapping for its exact target hostname
and port to the owned address. Other DNS lookups are refused. This supports
Cube's read-only `/etc/hosts` while preserving the original HTTPS URL, SNI and
certificate verification. The independent network guard remains authoritative.

The host activity captures the builder candidate immediately before mandatory
verification, retains its archive and inventory privately, then re-exports the
filesystem after verification. The two inventories must match. Dirty and
untracked source is included under the bounded export policy; excluded
credentials and outside symlink content are withheld. A redacted patch is
never used as executable candidate input. Verification failure, missing
verification evidence, capture failure, or filesystem mutation prevents an
assessment from producing complete advice.

Only small references, digests, and typed summaries enter Temporal history.
Source archives, inventories, request transcripts, and evidence bytes stay in
`<storage.data_dir>/security-private/<run-id>/`, outside the ordinary run
artifact namespace. Directories use mode `0700` and files `0600`. Candidate
snapshots are removed after assessment or fallback recording. Raw evidence is
retained privately for operator audit; apply an operator retention policy to
this directory. The normal run artifacts contain `security/assessment.json`
and, when verified, `security/manifest.json`, with bounded identifiers,
coverage, assertion outcomes, cleanup state, and evidence hashes.

The trusted executor owns fresh target/assessor allocation, route binding,
resource limits, execution, evidence export, and teardown. It receives private
candidate data only within the activity process. Assessment activities use
one attempt so Temporal retries cannot duplicate assessment requests. An
unconfigured executor records `executor_unavailable`; it never skips with an
implied pass. Partial coverage, invalid identity/evidence, persistence loss,
and cleanup failure remain visible as incomplete advice. A confirmed seeded
failure can have complete coverage and a failed assertion: coverage and the
security verdict are separate fields.

Advice is recorded after functional verification and before human review.
Passing advice cannot repair a failed mandatory gate, approve a change, or
override a human rejection or timeout. Missing ordinary assessment artifacts
are evidence loss and follow the factory's existing evidence failure handling.
The optional AI profile preserves the explicitly chosen provider/model;
unavailable model coverage is retained and never silently switched.

Candidate capture and assessment heartbeat to Temporal and wait for cancellation
acknowledgment. Capture leaves room for detached export cleanup within its
activity deadline. Cancellation propagates after private source and owned
executor resources are reconciled; the workflow stops before proceeding to
later stages. Test-suite cancellation mocks do not establish this ordering:
the integration tests exercise a real Temporal worker and withhold cleanup
acknowledgment while checking the workflow remains open.

The workflow uses change ID `security-assessment-advisory-v1`. Disabled runs
add no new activity or version marker; histories without the version retain
the previous command sequence. Tests cover disabled/current/old-version paths,
functional failure, advisory failure, persistence loss, and human review.
`TestSecurityReplayPreChangeHistory` performs SDK replay when
`SECURITY_REPLAY_HISTORY` points to a retained Temporal JSON export. The export
stays outside the repository because it can contain private metadata. A
missing export is a visible skipped test, not replay evidence. Record actual
new-history replay separately from version-branch tests.

Qualification records must distinguish source/build checks, local Cube
validation, exact candidate CI, independent human review, and live runtime
acceptance. Fixtures and an operator receipt schema do not qualify a route.
Keep the feature disabled until actual endpoint-only reachability and owned
resource cleanup have passed against the pinned runtime. Factory success
still does not constitute human approval or production acceptance.
