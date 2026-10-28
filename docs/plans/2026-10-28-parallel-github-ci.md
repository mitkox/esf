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

Baseline investigation found the `uv` step never used `uv`: the Makefile takes
its uv branch only when `uv python find 3.15.2` succeeds, so CI silently fell
through to `make test-python-docker` and built the intake Docker image on every
run (~59s of the 66s). The baseline workflow had no pinned interpreter for uv
to find, so the documented uv path had never run in CI.

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
- Add `astral-sh/setup-uv`, pinned by commit SHA and to the `uv` version in
  `.mise.toml`, with the uv cache and managed Python cached; install the pinned
  Python 3.15.2 once so `make test-python` takes its intended uv path instead
  of the Docker-image fallback. The fallback remains available as
  `make test-python-docker` and is still exercised by the candidate-image
  workflow.
- Keep the `check` gate job name (the only required status check on `master`)
  and make it require every new job.
- Add `concurrency` so a new push to a pull request cancels the superseded
  run; `master` push runs are never cancelled.
- No check is dropped, no existing command changes, no permission is widened,
  and every action remains pinned by commit SHA.

## Acceptance criteria

1. `check` is green on the validation pull request and every check from the
   previous workflow still runs exactly once.
2. Linux critical path is under 2 minutes measured from the Actions jobs API
   (baseline 2m44s), with end-to-end workflow wall time below the 2m54s
   baseline.
3. Branch protection is unaffected: the required `check` context still exists
   and still fails when any check fails.
4. Superseded pull-request runs cancel; `master` runs are not cancelled.

## Validation

All runs below are on draft PR #54 and ended with a green `check` gate.

| Run | Workflow under test | Wall time | Slowest job |
| --- | --- | --- | --- |
| [40642262249](https://github.com/mitkox/esf/actions/runs/40642262249) | baseline, serial | 2m54s | Linux 2m44s |
| [40644784628](https://github.com/mitkox/esf/actions/runs/40644784628) | parallel split, first run | 1m38s | Python 87s (uv cache miss) |
| same run, rerun | parallel split, warm cache | 1m23s | Python 72s (Docker fallback) |
| [40645297784](https://github.com/mitkox/esf/actions/runs/40645297784) | + pinned Python 3.15.2 for uv | 1m43s | macOS 91s |

Observations:

- The first parallel run proved the split: six hosted jobs started together,
  `check` gated them, and the serial Linux job's 2m44s became a 30-50s
  critical path.
- The rerun exposed the real Python cost: `make test-python` was building the
  intake image (59s) because `uv python find 3.15.2` failed. Installing the
  pinned interpreter dropped the whole Python job from 87s to 30s
  (`make test-python` now 12s).
- With Python and Go off the tail, the unchanged macOS job is now the critical
  path (60-90s, run-to-run hosted variance included), so end-to-end time
  approaches the macOS floor. Warm Linux-only wall time was ~1m23s; the
  slowest observed end-to-end run was 1m43s, still 41% below the 2m54s
  baseline.
- Per-job durations are the stable comparison because GitHub-hosted scheduling
  varies: Linux critical path went 2m44s -> ~50s, Python 66s -> 12s.

## Rollback

Revert the single workflow commit; the previous serial workflow is restored.
No repository settings, credentials, or infrastructure are changed.

## Cube verification

Not applicable: this change only reorders GitHub Actions jobs. It does not
modify factory, CubeSandbox, Temporal, or runtime behavior, so no Cube
acceptance run is added here. CI still builds the factory and runs the Go,
frontend, Pi, and Python suites that exercise that code.
