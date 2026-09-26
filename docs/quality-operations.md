# Quality operations and migration

## Running without QMS

QMS is opt-in: `quality.policy_files` must contain at least one policy file to
enable it. The default `factory.example.toml` has no quality configuration.
Without it, the factory runs its normal authoring, verification, patch export
and cleanup workflow, without opening a quality database or socket. The normal
review feature remains available; it does not create authenticated QMS approval.
No enterprise QMS account, adapter, license key or network service is required.

Scope `quality_policies` bindings require QMS to be enabled; an incomplete
configuration fails validation instead of silently removing required controls.
Do not disable QMS on workers serving active controlled runs. Finish those runs
first, retain their private database and objects, and use a separate task queue
for a standard deployment. Earlier ungoverned runs remain ungoverned.

With QMS enabled, the local provider and embedded SQLite are sufficient. External
requirements or acknowledgments become dependencies only when a policy declares
them. Concrete enterprise vendor adapters are not included in v1.

## Enablement

1. Use a dedicated Linux worker account. Put `storage.data_dir` on durable local
   storage; SQLite on a shared/network filesystem is not supported.
2. Register **all** repositories used by the deployment, including unprotected
   ones. Use stable IDs, exact remote aliases and/or canonical local paths.
3. Associate mandatory policies with operator scopes. A repository inherits the
   union from all scopes registered for it. Scope selection cannot remove them.
4. Bind real OS UIDs to roles. Keep submitter and approver accounts distinct
   where policy requires independence. Sharing an OS account shares identity.
5. Use a worker-owned socket directory such as `/run/esf`, mode `0750`. Grant
   traversal to the client group and configure `socket_gid`; the socket is
   `0660`. Neither the parent nor private database directory may be writable by
   clients. Do not grant clients the worker UID or database access.
6. Validate the configuration and YAML before switching the worker. All workers
   consuming the protected task queue must run the same QMS enforcement setup.

```toml
[quality]
policy_files = ["policies/qms.yaml"] # relative to the TOML file
socket = "/run/esf/quality.sock"
socket_gid = 2000
capa_roles = ["quality-authority"]

[quality.roles]
"1001" = ["submitter"]
"1002" = ["maintainer"]
"1003" = ["quality-authority"]

[quality.repositories.product]
aliases = ["https://github.com/example/product.git", "git@github.com:example/product.git"]
scopes = ["default", "regulated"]

[scopes.regulated]
quality_policies = ["qms"]
```

Quality configuration is optional. An enabled deployment rejects unknown or
ambiguous sources even when a submitted scope has no quality policy. Existing
Temporal histories retain their old behavior through workflow versioning;
allow those histories to finish, and submit a new controlled run for readiness.
Do not assign quality approval to their exported manifests.

The schema migrates transactionally when the worker opens the store. Version 1
creates `quality/quality.db` and `quality/objects` beneath the artifact root.
A database with a newer schema version is rejected. Back up the SQLite database
using SQLite's backup procedure (or stop the worker before copying the database,
WAL and objects together). Restore the database and immutable objects as one
unit. Deleting exports does not delete the authoritative result.

## Submission and queries

Normal `factory run` and `factory apply` route protected repositories through the
socket. A client can use a minimal local setup with the explicit socket:

```sh
factory quality --socket /run/esf/quality.sock submit -f request.json
factory quality --socket /run/esf/quality.sock runs
factory quality --socket /run/esf/quality.sock status RUN_ID
factory quality --socket /run/esf/quality.sock show RUN_ID
factory quality --socket /run/esf/quality.sock evidence RUN_ID
factory quality --socket /run/esf/quality.sock events RUN_ID
factory quality --socket /run/esf/quality.sock evaluate RUN_ID --paths docs/readme.md
factory quality policy validate policies/qms.yaml
```

`submit` accepts a `RunRequest` JSON object. Use one `repository` remote alias or
one `local_path`, plus `run_id`, `revision`, `task`, `agent_harness` and
`verification_profile`. IDs are idempotency keys: a different request or
requester cannot reuse one. Client configuration and actor labels have no
server authority. A direct Temporal submission for a protected repository fails
without the service's matching admission.

All quality commands return JSON. `status` explains risk, digests, missing
controls and the decision; `show` includes the frozen definitions and pending
approval request. Evaluation is read-only and cannot replace the actual
candidate's classification. Agents receive a safe provisional policy inventory
at `/workspace/.factory/quality-plan.json` during authoring.

Normal `status`, `get runs`, `describe runs` and change views consult the service
for controlled records. A service outage is reported rather than trusting a
stale compatibility export. A previous Change `DONE` result does not authorize
a later activation.

## Approvals and exceptions

```sh
factory quality --socket /run/esf/quality.sock approve RUN_ID \
  --operation approval-2026-001 --reason "Reviewed exact candidate and evidence"
factory quality --socket /run/esf/quality.sock reject RUN_ID \
  --operation rejection-2026-001 --reason "Required behavior is incorrect"
factory quality --socket /run/esf/quality.sock exception RUN_ID \
  --operation exception-2026-001 --control compatibility \
  --reason "Documented, bounded deviation accepted by the policy authority"
```

Commands first read the request and then submit its exact candidate, plan and
request identifiers. Supply a stable `--operation` when retrying an uncertain
response. Later decisions after a terminal approval/rejection/expiry fail.

Human approval timeout is policy-defined. A waivable failed gate receives the
shorter of that deadline and one hour for disposition. Satisfying disposition
retains the original human approval deadline. Only explicitly allowed
roles can waive it, and the requester cannot grant their own exception. Missing
evidence, identity, candidate integrity, policy immutability and audit durability
cannot be waived. Original failing attempts and non-conformances remain visible.

## Attestations and exports

```sh
factory quality --socket /run/esf/quality.sock attestation RUN_ID > statement.json
factory quality --socket /run/esf/quality.sock object sha256:DIGEST
factory quality --socket /run/esf/quality.sock outbox
```

The statement uses [in-toto Statement v1](https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md)
with an ESF patch-readiness predicate. V1 statements are unsigned; consumers
must obtain them from the trusted authority. Only the exact `changes.patch`
digest is the subject. Baseline, candidate, policies, plans, attempts, evidence,
approvals and exceptions appear in the predicate. This proves no release
authorization for a built artifact or deployment target.

The outbox retries dispatch, approval wakes, JSON exports and provider
synchronization. Failed exports remain pending without changing the terminal
decision. Restore disk/provider availability and keep the worker running to
repair them. A conflicting pre-existing Temporal workflow ID stays visibly
pending until a matching admission binds; investigate it and use a new run ID
when it belongs to a different execution. Do not delete immutable records.

## CAPA

```sh
factory quality --socket /run/esf/quality.sock nc
factory quality --socket /run/esf/quality.sock capa open -f capa-open.json
factory quality --socket /run/esf/quality.sock capa act CAPA_ID -f action.json
factory quality --socket /run/esf/quality.sock capa CAPA_ID
```

Opening input: `id`, `run_id`, `non_conformance_id`.
Every action has `operation`, `action`, and `reason`:

| Action | Additional input and rule |
|---|---|
| `plan` | `root_cause`, `acceptance_criteria`, `corrective_actions`, `preventive_actions` |
| `approve-plan` | Actor differs from creator and plan author |
| `link-corrective` / `link-preventive` | `run_id` of a controlled remediation run |
| `verify` | `criteria_digest` from the approved plan, `evidence_digests` selected from approved remediation; reason records the effectiveness assessment |
| `close` | Authorized actor differs from creator and verifier |

The independent verifier must differ from the creator, plan author/approver
and remediation requesters. Both corrective and preventive runs must be linked
and approved. V1's effectiveness assessment is an authenticated human judgment
against explicit criteria and selected evidence. The Temporal CAPA workflow
survives restarts and continues as new to bound history growth. Closure never
changes the original execution's failed decision.

## Operations and limits

`factory_quality_records` reports durable counts by kind, status and risk:
gates, approvals, exceptions, missing obligations, NC, CAPA and outbox backlog.
Gate duration and approval-wait histograms are execution observations; the
database remains the source for exact counts and audit reconstruction.

Controlled transport rejects submodules, `.gitmodules`, Git filters and LFS
materialization. It accepts ordinary files, executable modes, symlinks and
binary patches. Large objects over 256 MiB or snapshots over 1 MiB fail visibly.
Secret-bearing authoritative payloads are rejected with a safe digest diagnostic;
diagnostic logs can be redacted and cannot satisfy required machine evidence.

After an uncertain actor execution, submit a new run. The factory will not
rerun an author or convert an unknown verdict into success. Cleanup failures
prevent quality success and human approval; inspect ownership tags (`run_id`,
`workflow_run_id`, `quality_slot`) when repairing provider availability.
