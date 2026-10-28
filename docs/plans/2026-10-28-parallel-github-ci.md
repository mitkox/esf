# Parallelize the GitHub-hosted CI workflow

- Date: 2026-10-28
- Status: implemented on `ci/parallelize-hosted-ci`, stacked on
  `feat/enterprise-factory-console` (PR #53) so the change lands on the same
  workflow version that PR introduces.
- Owner: host Codex session; independent review requested from `esf-reviewer`.

## Problem

`.github/workflows/ci.yml` ran every Linux check in one serial `linux` job.
GitHub-hosted runners give each job its own 4-vCPU machine, so the serial job
paid the sum of all check durations while the independent macOS job finished
with the runner idle.

Baseline: run [40642262249](https://github.com/mitkox/esf/actions/runs/40642262249)
(commit `1f9311c`, 2026-10-28): workflow 2m54s end to end, `Linux checks`
2m44s, `macOS checks` 1m03s, `check` 3s.

Largest Linux steps from that run:

| Step | Duration |
| --- | --- |
| Test DSPy intake without credentials (`uv sync --locked`) | 66s |
| Build and test pinned Pi runner | 27s |
| actions/setup-go (cache restore) | 20s |
| actions/setup-node (cache restore) | 9s |
| Run Go tests with the race detector | 16s (68s on a cold cache) |
| Run Go vet / Check Go vulnerabilities | 3s / 3s |
| Scan source for secrets (cached `go run` build) | 4s |
| Build factory | 3s |

The `uv` step is uncached: the workflow relied on whatever `uv` the hosted
image provides and re-downloaded the managed Python and every locked
dependency on each run.

## Change

- Split the serial `linux` job into five independent jobs, each with its own
  checkout and only the toolchain it needs:
  - `go`: gofmt, `go vet`, `go test -race ./...`, build `bin/factory`.
  - `security`: gitleaks source scan, `govulncheck`.
  - `frontend`: issue-triage node test, console `npm ci`/test/build/dist
    freshness/audit, OpenCode lockfile audit.
  - `pi`: pinned Pi runner build/test/audit/dist diff.
  - `python`: release inventory, qualification boundaries, Temporal restored
    database safety, Pi inventory, SBOM, `make test-python`.
- Cache `uv` and the managed Python with `astral-sh/setup-uv` pinned by commit
  SHA and to the `uv` version in `.mise.toml`.
- Keep the `check` gate job name (the only required status check on `master`)
  and make it require every new job.
- Add `concurrency` so a new push to a pull request cancels the superseded
  run; `master` push runs are never cancelled.
- No check is dropped, no command is changed, no permission is widened, and
  every action remains pinned by commit SHA.

## Acceptance criteria

1. `check` is green on the validation pull request and every check from the
   previous workflow still runs exactly once.
2. Linux critical path is under 2 minutes measured from the Actions jobs API
   (baseline 2m44s), with end-to-end workflow wall time below the 2m54s
   baseline.
3. Branch protection is unaffected: the required `check` context still exists
   and still fails when any check fails.
4. Superseded pull-request runs cancel; `master` runs are not cancelled.

## Rollback

Revert the single workflow commit; the previous serial workflow is restored.
No repository settings, credentials, or infrastructure are changed.

## Cube verification

Not applicable: this change only reorders GitHub Actions jobs. It does not
modify factory, CubeSandbox, Temporal, or runtime behavior, so no Cube
acceptance run is added here. CI still builds the factory and runs the Go,
frontend, Pi, and Python suites that exercise that code.
