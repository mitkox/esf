# Policy, gate and provider guide

## Policy schema

YAML uses `apiVersion: esf.io/v1`, `kind: QualityPolicy`, a unique metadata
`name` and immutable `version`. Unknown fields and multiple documents fail.
The Go schema in `internal/assurance/policy.go` is authoritative. Policy files
are resolved relative to their referring TOML file.

The starter vocabulary is R0–R4. Documentation-only R0 uses `all_paths: true`;
unmatched paths default to R2. R1/R3/R4 are selected only by explicit rules.
`risk.floor` can raise, never lower, the result. Across policies the highest
risk selects the union of controls/evidence and all approval obligations.
Conflicting definitions fail; each risk needs an explicit approval rule.

Patterns support `*`, `**` and `?`, anchored to repository-relative paths.
No match or evidence path may escape the repository. Candidate path discovery
includes both sides of renames and tracks file modes/types through reconstruction.

## Gate contracts

| Type | Configuration | Passing evidence |
|---|---|---|
| `verification` | `profile`: frozen TOML command profile | Factory-collected result; every mandatory step succeeds |
| `independent-review` | `harness`: distinct qualified configuration; optional `report_file` | Strict `ReviewReport` bound to candidate and plan |
| `provider-snapshot` | `provider_kind`: `requirement` or `change`; immutable `reference` | Required fact retrieved and frozen at admission |
| `provider-ack` | `reference` label and `provider_timeout` (positive, at most 30 days) | Imported acknowledgment with the exact candidate and plan digests and `acknowledged: true` |

`risks` restricts applicability; omitted means all risks. `optional` controls are
recorded but do not gate readiness. `outputs` maps evidence kind to a newly
generated repository-relative file. Existing candidate files cannot be declared
outputs. Output files are collected before source mutation checking; all other
nonignored source changes fail the gate. Output kinds cannot impersonate
`baseline`, `patch`, `gate-result`, `author-result`, `review-report`, `cleanup` or
`qualification` receipts.

Fresh sandboxes use the admitted workspace/template. Ordinary verification
does not provision the author's model credentials. A scanner or SBOM generator
is an operator-configured verification profile plus tool qualification and
required output evidence. A missing executable, digest mismatch, failed command
or missing report blocks the control. For example:

```yaml
gates:
  - id: sbom
    type: verification
    profile: qualified-sbom
    risks: [R2, R3, R4]
    outputs: {sbom: .quality-output/sbom.json}
qualifications:
  - id: approved-sbom-binary
    phase: verification
    executable: /opt/quality/bin/sbom-tool
    binary_sha256: REPLACE_WITH_ACTUAL_64_HEX_DIGEST
    risks: [R2, R3, R4]
evidence: [sbom]
```

Use the tool's real absolute executable in the profile. The placeholder digest
above deliberately fails validation. Qualified tool bytes are measured before
and after the command; provision them in the approved workspace or fixed setup.
Package names alone do not constitute a binary qualification.

Author prerequisites are established before authoring, including configured
risk-specific prerequisites that could later apply. Qualifications can bind a
harness, model, template and/or observed executable digest. A final plan cannot
retroactively supply absent author provenance.

## Independent review

Review uses a fresh actor/session/sandbox and a different harness configuration
name. Sharing the underlying implementation/model is allowed unless
`different_model: true` requires distinct known effective model identities.

```json
{
  "candidate_digest": "sha256:...",
  "plan_digest": "sha256:...",
  "verdict": "approve",
  "findings": []
}
```

Verdicts are `approve` or `changes_requested`. Findings must be a JSON array.
Wrong digests, extra fields/content, malformed reports and exit-code-only
success fail. Raw stdout is suitable only for a harness that emits precisely
this object. For event-stream CLIs, configure
`report_file: /workspace/.factory/review-report.json`; the reviewer writes only
the report file and the factory parses it. Process stdout/stderr remain
diagnostic evidence.

V1 supplies a no-source-edit instruction, uses a disposable reviewer clone,
detects source mutation and never imports reviewer source changes. There is no
claim of enforced guest filesystem isolation. `filesystem_read_only: true`
fails configuration validation until a qualified provider capability exists.
Configure editing tools off in harnesses that offer a suitable review mode.

## Provider contract

`assurance.QMSProvider` defines requirement/change retrieval, quality/evidence
publication, non-conformance/CAPA operations, approval status and the separate
future release boundary. The local implementation uses the authoritative store;
provider receipts are immutable projections. Vendor-specific implementations
are intentionally absent in v1.

```sh
factory quality --socket /run/esf/quality.sock import requirement REQ_42 -f requirement.json
factory quality --socket /run/esf/quality.sock import change CHANGE_42 -f change.json
factory quality --socket /run/esf/quality.sock import acknowledgment ACK_42 -f acknowledgment.json
```

Only `quality-authority` can import facts. Imports retain the authenticated
actor and cannot overwrite the same kind/ID with different bytes. Required
retrievals become frozen admission dependencies. Required acknowledgments are explicit controls. After sandbox cleanup, the
workflow publishes a durable acquisition request in the run checkpoints with
a reference of `RUN_ID.REFERENCE`, candidate/plan digests and a server deadline.
Import to that exact per-run reference and include `run_id` for an outbox wake.
The workflow also polls durably; a timely imported response remains eligible
when its notification is delayed. Evidence binds the exact candidate and plan.
Missing, late or mismatched acknowledgments fail closed. Optional unavailable
provider snapshots are recorded as failed optional controls and do not block
admission.

For a future external adapter, dispatch mutations through the durable outbox
with stable idempotency keys. Never make a network callback authoritative for
local approval. Import the external fact, verify its binding and satisfy the
declared control. Keep optional exports visibly pending when the provider is
down. Keep required acknowledgments in the readiness decision.

## Validation

```sh
go test -race ./...
go vet ./...
```

## Integration tests

Use a Linux host and a dedicated local Temporal test server (`make temporal-up`).
The test client defaults to `127.0.0.1:7233` and namespace `default`, overridden
by `TEMPORAL_HOST_PORT` and `TEMPORAL_NAMESPACE`. It uses a fixture payload codec
and is not configured for a production Temporal connection.

Use an operator-approved scratch Cube template and network endpoint. The tests
create real VMs and can incur infrastructure costs; no model API is needed.
Do not commit credentials or generated run data.

```sh
CUBE_API_URL=http://127.0.0.1:4000 CUBE_TEMPLATE_ID=YOUR_READY_TEMPLATE \
  CUBE_PROXY_NODE_IP=YOUR_PROXY_IP \
  go test -race -tags=integration ./internal/factory \
  -run '^TestQuality(CubeR2R3|ReplaysLegacyHistory)$' -v -count=1 -timeout=35m
```

The live integration test provisions real VMs with controlled test harnesses,
executes R2 and R3, checks cleanup before socket-authenticated approval, then
restarts the worker/database during the R3 approval wait, and replays completed
histories with the matching payload encryption codec. The legacy replay test
generates a pre-QMS history using a fixture provider; it reads no production history.
