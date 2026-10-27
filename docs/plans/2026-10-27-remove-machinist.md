# Remove the inherited Machinist surface

Status: implemented and independently reviewed on branch `remove-machinist`
(2026-10-27); review fixes applied. Baseline: `fd52e72` (`origin/master`,
"chore(deps): refresh ESF components and pinned build images (#49)").

## Goal

Make `factory` the only product surface. Delete the inherited `machinist` CLI,
local control plane, embedded web UI/console, managed worker, SQLite store and
schema, updater, GitHub/cron triggers, examples, skill, `agent.py` workflow and
evals. Keep factory runtime code, config schema, evidence layout, agent runner
assets and historical records unchanged.

## Decisions

- Keep the unreleased `v0.6.2` candidate; record the removal as a breaking
  change in `docs/release-v0.6.2.md`. If 0.6.2 is already published elsewhere,
  re-target to `v0.7.0` and apply the same version-pin deltas.
- Do not replace the console. Document `factory describe`, `status --watch`,
  `logs`, `get changes` and Temporal's UI as alternatives. A small factory-only
  evidence viewer is a possible follow-up.
- No Machinist data migration: the factory never read `~/.machinist` state.
- Preserve `LICENSE`, `docs/upstream.md` attribution, historical release notes,
  reviews, benchmarks and qualification records.
- Keep the Node toolchain for the optional Pi/agent packaging and keep the
  independent issue-triage demo.

## Change summary

- Go: deleted `cmd/machinist`, `internal/{cli,config,controlplane,managedworker,
  runner,updater,artifacts,protocol,triggers}`, `examples`, `evals`, `agent.py`,
  `skills/machinist`; `go mod tidy` removed `modernc.org/sqlite` and transitives.
- Web/assets: removed the embedded React/Vite console and Machinist SVG assets.
- Build/release/CI: factory-only Makefile, `install.sh`, Dockerfile target,
  release scripts, inventories, SBOM generator, qualification and promotion
  checks; removed frontend and `Build Machinist` CI steps; removed console and
  managed-worker image matrix entries and the controlplane/web dependabot block.
- Deploy: deleted the console Helm template/values/PVC/ServiceAccount wording,
  Machinist systemd units, managed-worker image and the VM bootstrap script;
  updated the chart README and upgrade guidance.
- Docs: rewrote README, ARCHITECTURE, FACTORY, development, CONTRIBUTING,
  SECURITY, docs index, upstream history, production deployment/readiness,
  release notes and issue-triage demo; deleted the ten inherited Machinist
  guides; added ADR 0008 and this plan.

## Verification

Run from a clean worktree based on `origin/master`:

- `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`,
  `gofmt -l`, `make lint`.
- Test-package compilation (`go test -c -o /dev/null` for every package, with
  and without the `integration` tag) without running tests on the host.
- `python3 scripts/verify-release-inventory.py`,
  `python3 -m unittest discover -s scripts/tests`, SBOM generation.
- `bash scripts/release.sh v0.6.2-test.1 DIR`, repeated into a second directory
  and compared with `diff -qr`; only `esf_factory_*` archives are produced.
  `bash scripts/verify-release.sh` passes on the dry-run set (reviewer
  reproduction).
- `helm lint` and `helm template` with required dummy values; no console,
  managed-worker or other Machinist resources render.
- `sh -n install.sh`, `shellcheck install.sh scripts/*.sh`; the removed
  component path fails with guidance.
- Reference sweep for `machinist`, deleted package paths and deleted docs; only
  intentional attribution/migration/historical references remain.
- Two independent reviews found no blocker or high-severity defect; findings
  were addressed (pin-table note, host/cluster state guidance, archive vs image
  wording, ADR path/percentage corrections, release-usage wording).

The Go unit and race suites are authoritative in CI (Linux and macOS). They were
not executed on the host, per the repository execution policy; all test packages
were compiled locally. No Cube runtime validation was performed because no
factory execution path changed. CI results are attached to the PR.

## Limits and follow-ups

- The console's read-only factory evidence view is removed with no replacement.
- Old companion qualification manifests bind archives/images that no longer
  exist; the release candidate must be rebuilt and qualified from scratch.
- Historical documentation keeps pre-removal statements by design; ADR 0002 and
  ADR 0008 explain the change.
- Local-only `.github/site`, `pages.yml` and worktree directories are untouched.

See `release/qualification/machinist-removal-2026-10-27.md` for the recorded
removal evidence.
