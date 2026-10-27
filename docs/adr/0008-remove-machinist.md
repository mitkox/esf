# ADR 0008 — Remove the inherited Machinist surface

- **Status:** Accepted
- **Date:** 2026-10-27
- **Deciders:** ESF maintainers
- **Supersedes:** the coexistence assumptions in
  [ADR 0002](0002-use-temporal-for-durable-workflows.md); earlier ADRs remain
  valid as historical design rationale.

## Context

ESF began as a fork of [Machinist](https://github.com/owainlewis/machinist) and
inherited its `machinist` CLI, local control plane, SQLite job store, managed
worker, updater, GitHub/cron triggers and embedded web UI. The factory layer was
added beside that app.

By 0.6.2 the two surfaces had become fully independent:

- no factory package imports a Machinist package, and no Machinist package
  imports the factory; `go list` confirms disjoint dependency trees;
- the factory archive and installer default to `factory`, which needs neither
  the control plane nor the console at runtime;
- the console's only factory integration is an optional read-only evidence view
  configured with `[factory_read]`; it is not on the execution path;
- ESF operators used the Machinist UI for a small number of demo jobs while the
  factory completed hundreds of runs through its CLI; the control-plane state
  was last written in early October 2026.

Keeping the inherited app also carried costs disproportionate to that use:

- roughly 39% of the Go code and the only embedded React/Vite frontend, with
  Node/npm build, audit and dependabot machinery in CI;
- a SQLite dependency and schema migrations;
- extra release archives (`machinist`, `combined`), container images (`console`,
  `managedworker`), Helm resources, systemd units and VM bootstrap scripting;
- its own qualification axis in the release inventory;
- a second agent execution model that runs commands on the worker host with the
  worker user's permissions, outside the CubeSandbox isolation that ESF's
  security model depends on;
- a loopback HTTP control plane, browser session, bearer tokens, updater and
  GitHub/cron triggers that all need security review and documentation.

The factory deliberately has no web UI: the CLI is the control plane and the
security boundary.

## Decision

Remove the inherited Machinist implementation and documentation from ESF. The
`factory` binary, CLI, configuration schema, Temporal workflow and evidence
layout remain unchanged. ESF ships one archive per platform and one factory
image. `machinist`, `machinist-control-plane` and the console are no longer
supported surfaces.

The removal is a breaking change relative to 0.5.0: `esf_machinist_*` and
`esf_combined_*` archives, the console/managed-worker images, Helm console
values and Machinist state are discontinued. No data migration is provided
because the factory never read Machinist state.

Attribution and history are retained: the original Machinist copyright remains
in [LICENSE](../../LICENSE), and [docs/upstream.md](../upstream.md) records the
fork relationship. Historical release notes, reviews, benchmarks and
qualification records are not rewritten.

## Consequences

- The release/CI/qualification/documentation surface shrinks to the factory.
- The optional read-only factory evidence console is lost. Operators use
  `factory describe`, `status --watch`, `logs` and `get changes`, or Temporal's
  UI for workflow state. A small factory-only evidence viewer remains a possible
  follow-up; it is not part of this decision.
- Operators must stop/remove Machinist units and Helm resources during upgrade;
  the installer rejects removed components with explicit guidance.
- Users who still want Machinist for local task-to-PR work can install upstream
  Machinist separately; ESF no longer vendors it.
- Earlier ADRs that reference Machinist are retained as historical reasoning;
  ADR 0002 carries an update note pointing here.
