# Component upgrade, 2026-10-27

Baseline: `ba829056ab728b8e002dddcb702edc0be5b19a1d` (GitHub master).
Implementation uses native Codex in an isolated clone; the dirty primary
checkout is excluded from publication.

Refresh official stable release metadata for maintained component and toolchain
pins. Upgrade newer direct dependencies, refresh affected lockfiles and generated
assets, and synchronize release inventories, CLI hashes, recipes and documentation.
Keep existing provenance and production qualification boundaries explicit.

Run dependency installation, generated builds and affected tests inside the local
Cube stack. Verify release and Pi inventories, Go module graph, Python lock,
frontend tests/build and secret scanning against the final candidate. Record exact
checks, remaining coverage and owned sandbox cleanup. Obtain independent scoped
review before publication. Push an upgrade branch and open a GitHub PR.

Upgrade the local managed tools using the existing updater. Reconcile deployed
ESF service versions with current stable pins, retaining data volumes and rollback
information. Apply Ubuntu updates when administrator authentication is available;
otherwise retain that precise blocker. Do not reboot without a separate request.

Acceptance: synchronized latest stable pins, passing affected Cube checks,
independent review receipt, verified GitHub branch/PR, recorded local upgrade
results and zero owned sandbox leaks. CI, deployment and production qualification
remain separate evidence.
