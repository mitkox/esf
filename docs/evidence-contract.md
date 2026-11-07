# Change-evidence contract

A change-evidence bundle is a signed record of one completed factory run. It
answers, without trusting the agent, the forge or a network service: which
inputs ran, against which base and resulting commits, under which agent and
image, which deterministic gates ran with which commands and results, and what
the evidence explicitly does not claim.

The bundle is a [DSSE](https://github.com/secure-systems-lab/dsse) envelope
around an [in-toto Statement](https://in-toto.io/Statement/v1). It verifies
offline with the published public key and needs no certificate authority or
transparency log.

## Envelope

```json
{
  "payloadType": "application/vnd.in-toto+json",
  "payload": "<base64 standard encoding of the statement>",
  "signatures": [
    {"sig": "<base64 standard encoding of the ed25519 signature>"}
  ]
}
```

The signature covers the DSSE v1 pre-authentication encoding:

```
PAE = "DSSEv1" SP len(payloadType) SP payloadType SP len(payload) SP payload
```

A verifier must reject an envelope whose payload type differs, whose payload
does not match any signature under the supplied public key, or whose statement
or predicate type is not the versioned type below.

## Statement

```json
{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [
    {"name": "changes.patch", "digest": {"sha256": "<hex>"}},
    {"name": "git-commit", "digest": {"sha1": "<hex>"}}
  ],
  "predicateType": "https://esf.dev/evidence/change/v1",
  "predicate": { "...": "..." }
}
```

- The `changes.patch` subject digests the exact patch artifact the run
  produced.
- The `git-commit` subject carries the resulting commit id under the algorithm
  its length implies: forty hex characters use `sha1`, sixty-four use
  `sha256`. It is absent when the run produced no commit.
- The predicate type's final path segment is the contract major version. A
  breaking change moves it; additions within a major version do not.

## Predicate

| Field | Meaning |
| --- | --- |
| `run.run_id` | The factory run identity |
| `run.workflow_id` | The durable workflow identity |
| `run.change_id`, `run.parent_run_id` | Durable work-item and lineage |
| `run.factory_version` | Factory implementation version |
| `run.state` | The recorded factory result |
| `run.started_at`, `run.completed_at` | Run timestamps (RFC 3339) |
| `input.repository` | The approved repository |
| `input.requested_revision` | The exact revision requested |
| `input.base_commit` | The baseline the patch was produced against |
| `input.result_commit` | The commit produced by applying the patch, when one exists |
| `input.task_sha256` | Digest of the exact task bytes shown to the agent |
| `agent.harness`, `agent.version` | Agent harness identity |
| `agent.model` | Model identity |
| `agent.sandbox_provider`, `agent.image` | Sandbox provider and image identity |
| `verification.profile` | Verification profile name |
| `verification.passed` | Whether every mandatory gate passed |
| `verification.gates[].name` | Gate name |
| `verification.gates[].command` | The exact argument vector, never a shell string |
| `verification.gates[].passed` | Gate result |
| `verification.gates[].timed_out`, `exit_code` | Why the gate ended |
| `verification.gates[].duration` | Gate duration in nanoseconds |
| `verification.gates[].stdout_sha256`, `stderr_sha256` | Digests of the recorded gate logs |
| `policy_digest` | Digest of the resolved operator policy the run executed under |
| `non_claims` | What the bundle does not establish |

Timestamps are RFC 3339 when encoded by Go's `time.Time` JSON form. Digests are
lowercase hexadecimal SHA-256 unless a field names another algorithm.

### Non-claims

Every bundle carries the same explicit non-claims:

- It attests only to the run, input snapshot and gate results recorded in it.
- It does not claim that the change is correct beyond the named gates, or that
  an agent's reasoning or output is trustworthy.
- It does not claim forge acceptance, merge safety, deployment, production
  readiness, or behavior outside the recorded run.

## Signing

The factory holds an ed25519 private key outside every sandbox. The key is read
through the private-file rules: a regular file, no symlink, owner-only
permissions, bounded size. It is never mounted into a Cube microVM, injected
into an environment, or written into evidence.

- Private key file: PKCS#8 PEM (`-----BEGIN PRIVATE KEY-----`).
- Public key file: PKIX PEM (`-----BEGIN PUBLIC KEY-----`).
- One signature is produced per bundle. Consumers may accept a bundle when any
  signature verifies under the key they trust.

A private key leaked to an agent would let the agent forge a "verified" claim.
The signer runs on the factory host after verification; neither the agent's
output nor a repository file is an input to the signature.

## Verifying

```
factory verify --bundle evidence/bundle.json --key esf-evidence-public.pem
```

The command performs no network access. It prints the run, the subject digests
and the gate summary, then exits non-zero if the signature, statement type,
predicate type or key does not verify. A changed gate result changes the signed
payload and fails verification.

The key path is read through the private-file rules, so the verifier must own
the public key file with mode `0600` even though a public key is not secret.
Copy it into place with those permissions before verifying.

## Compatibility

- Within the `v1` predicate type, fields may be added. Consumers must ignore
  fields they do not know; a verifier must not reject a bundle merely because
  its predicate carries an unknown field.
- Removing or renaming a field, changing a field's meaning, or changing the
  envelope or statement shape is breaking and requires a new major predicate
  type with a documented compatibility note.
- The envelope is standard DSSE, so a customer running Sigstore tooling can wrap
  the same statement with their own signatures. ESF does not depend on Sigstore,
  a CA, a transparency log or a hosted attestation service to verify its own
  bundles.
- The bundle is evidence, not the patch and not the logs. The patch and log
  artifacts live in the run's durable artifact directory; the bundle carries
  their digests.
