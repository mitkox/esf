# Concepts

Shared domain vocabulary for this project — entities, named processes, and status concepts with project-specific meaning. Seeded with core domain vocabulary, then accretes as ce-compound and ce-compound-refresh process learnings; direct edits are fine. Glossary only, not a spec or catch-all.

## Candidate assessment

### Candidate snapshot

The exact bounded application bytes and filesystem inventory captured from a builder for assessment, including permitted dirty and untracked content rather than only its Git revision.

### Assessment profile

An operator-owned selection of the runtime, permitted tools, required assertions and resource bounds that a run may request only through an explicit scope grant.

### Assessment receipt

Evidence binding assertion outcomes and coverage to a candidate snapshot, target, requests and runtime, together with the state of owned-resource cleanup.

Serialization does not preserve verification trust: transferred evidence must be checked against independently held identities and hashes before a receipt can establish a pass.

### Runtime qualification

Evidence that a selected assessment runtime and restricted target route support their declared operations and isolation, distinct from an application security verdict or human approval.
