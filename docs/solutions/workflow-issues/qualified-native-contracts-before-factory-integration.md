---
title: Qualify native contracts before expanding factory integration
date: "2026-10-27"
category: workflow-issues
module: Factory development infrastructure
problem_type: workflow_issue
component: development_workflow
severity: medium
applies_when:
  - "Adding a pinned execution backend or security assessment adapter"
  - "Building fake transport fixtures for an external tool contract"
tags: ["cube", "opencyber", "mcp", "qualification", "contract-tests"]
---

# Qualify native contracts before expanding factory integration

## Context

During [issue #50](https://github.com/mitkox/esf/issues/50), passing fake
transport tests did not establish that the adapter could consume the pinned
backend. Actual Cube execution exposed assumptions that the fakes had encoded:
the local template API exposed an ext4 artifact rather than the SDK's OCI image
field; Python's strict verifier rejected a certificate chain accepted by the
Go test client; and the log tool returned a report locator rather than inline
events. Repeating the fixture assertions could not reveal those differences.

These observations describe the issue candidate's local qualification, not a
released integration.
Operational details belong in the [runtime guide](../../../deploy/cube/security-runtime/README.md),
[endpoint runbook](../../../deploy/cube/README.security.md), and
[assessment guide](../../security-assessment.md). This learning retains the
development sequence that avoided counting those green fixtures as acceptance.

## Guidance

Establish one small native baseline inside the selected Cube template before
expanding adapter fixtures or wiring the workflow. Retain the actual startup,
required-tool discovery, one allowed target request, one generated report,
evidence export and external hash check. Inspect the exact pinned backend's
returned envelope and its referenced bytes. Build successful fake responses
from that observed contract; use negative fixtures to exercise bounds,
missing evidence, changed identities and unknown acknowledgments.

Keep separate evidence for the endpoint boundary, native backend operations,
candidate transfer, and workflow behavior. A successful route probe does not
exercise native HTTP client resolution or report consumption. An in-memory
Temporal cancellation test does not establish real activity cancellation
delivery. Both distinctions caused additional work in this run.

## Why This Matters

A fake is useful for controlling failures, but its author also controls the
shape of the success response. When implementation and fixture share an
incorrect assumption, both can agree while the real integration remains
unusable. The first native run should reduce that uncertainty before the
factory adapter grows around it.

The pinned timeline response reported complete analysis, yet the first adapter
found no denial because it inspected the wrong location. The repaired adapter
reads the bounded report and binds its source and records to the copied logs
(`internal/securityassessment/runner.go:499`). Summary-only output is
insufficient. Strict UTF-8 and JSON framing are checked before normalization
(`internal/securityassessment/runner.go:421`), so malformed source cannot be
silently converted into complete evidence.

## When to Apply

- Changing a runtime image, SDK, protocol library or selected tool version.
- Introducing an adapter whose fixtures currently describe an expected result.
- Promoting a transport or mock result into a workflow acceptance claim.

## Examples

For this integration, the live template check reads the actual artifact
metadata (`internal/securityassessment/cube_executor.go:499`), the TLS chain
uses the issued CA's generated identifier (`internal/securityassessment/route/broker.go:277`),
and native timeline fixtures now supply a report locator and file rather than
inventing inline events (`internal/securityassessment/runner_test.go:83`).

The private three-pair pilot retained successful legitimate requests, seeded
tenant disclosures, replacement denials, correlated denial logs and externally
verified exports. Real Temporal tests separately retained cancellation ordering
and enabled/disabled SDK replay. See the [qualification report](../../../release/qualification/opencyber-cube-pilot.md)
for the evidence identities and limits. These are local observations; they do
not establish human approval, representative findings or production readiness.

## Related

- [Implementation plan](../../plans/2026-10-27-1006-feat-opencyber-factory-assessment-plan.md)
- [Reproducible native pilot](../../../tools/cube-security-pilot/README.md)
