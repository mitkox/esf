# Machinist removal qualification — 2026-10-27

Candidate source: branch `remove-machinist`, based on `origin/master`
`fd52e72a09f85502e00e797c162d93a2a90a5e37`. This record covers the removal
change only; it is not production qualification for a release asset set.

## Scope

Removed: the inherited `machinist` CLI, local control plane, embedded web
UI/console, managed worker, SQLite store/schema 5, updater, GitHub/cron
triggers, `agent.py` issue-to-PR workflow, examples, skill, Machinist docs, the
`console`/`managedworker` images, Machinist/combined release archives, Machinist
systemd units and VM bootstrap script, and the console Helm resources.

Unchanged: factory code paths (`cmd/factory`, `internal/factory`,
`internal/agentharness`, `internal/sandbox`, `internal/verification`,
`internal/repository`, `internal/factoryartifacts`, `internal/threatmon`,
`internal/turnexec`), factory config schema, Temporal workflows, `.factory`
evidence layout, agent runner assets, Helm factory workload and NetworkPolicy,
`deploy/factory-worker.service`.

## Verification evidence

Recording commands and observed results. Host runs are builds/static tooling;
the Go unit and race suites remain CI-authoritative.

| Check | Result |
| --- | --- |
| `go build ./...` (Go 1.27.3) | pass |
| `go vet ./...`, `go vet -tags=integration ./...` | pass |
| Test-package compilation for every package with and without `integration` tag (`go test -c -o /dev/null`) | pass; no test references removed packages |
| `gofmt -l` / `make lint` (gofmt, vet, shellcheck) | clean |
| `python3 scripts/verify-release-inventory.py` | pass ("release inventory pins match lockfiles") |
| `python3 -m unittest discover -s scripts/tests -p 'test_*.py'` | 3 tests pass (factory-only archive fixture) |
| `python3 scripts/generate-sbom.py` | CycloneDX 1.6 document generated |
| `bash scripts/release.sh v0.6.2-test.1 DIR` then repeat into `DIR-repeat` | pass; `diff -qr` clean; four `esf_factory_*` archives + manifest/inventory/SBOM/checksums only |
| Archive contents | `LICENSE`, HARNESS2 license/notice/upstream, `README.md`, `factory.example.toml`, `factory` |
| `helm lint deploy/helm/esf` | pass |
| `helm template` with required dummy values | factory Deployment, ServiceAccount, PVC and NetworkPolicy only; no console/managed-worker resources |
| `sh -n install.sh`; `shellcheck install.sh scripts/*.sh` | pass |
| `ESF_COMPONENT=machinist sh install.sh` | fails before download with migration guidance |
| Reference sweep (`machinist`, `cmd/machinist`, `internal/controlplane`, deleted doc paths) | only intentional attribution, migration, historical and plan references |

## Limits

- The canonical Go unit and race suites were not executed on the host, following
  the repository execution policy. They must be green on the PR CI check before
  merge; this local record proves compilation and static checks only.
- CubeSandbox runtime validation was not performed: the change does not alter a
  factory execution path, and the MCP sandbox endpoint was unavailable
  (`UnknownIssuer`). No sandbox leak claim is made.
- The release dry-run used the `-test.` prerelease suffix and synthetic
  `v0.6.2-test.1` assets; it is not a qualified RC and no companion
  qualification manifest was produced.
- Historical release notes, reviews, benchmarks and qualification records were
  intentionally left unchanged.
- The console's read-only factory evidence view has no replacement.

## Review

Independent review: pending at time of writing; findings to be recorded with the
candidate file list before merge. Merge and publication remain separate human
approvals.
