# Parallelize the GitHub-hosted CI workflow

- Date: 2026-10-28
- Status: implemented on `ci/parallelize-hosted-ci`, stacked on
  `feat/enterprise-factory-console` (PR #53) so the change lands on the same
  workflow version that PR introduces.
- Owner: host Codex session; independently reviewed by `esf-reviewer`.

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
| Test DSPy intake without credentials (Docker fallback; `uv sync` runs inside the image) | 66s |
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

- Split the serial `linux` job into four independent jobs, each with its own
  checkout and only the toolchain it needs:
  - `go`: gofmt, `go vet`, gitleaks source scan, `govulncheck`,
    `go test -race ./...`, build `bin/factory`.
  - `frontend`: issue-triage node test, console `npm ci`/test/build/dist
    freshness/audit, OpenCode lockfile audit.
  - `pi`: pinned Pi runner build/test/audit/dist diff.
  - `python`: release inventory, qualification boundaries, Temporal restored
    database safety, Pi inventory, SBOM, `make test-python`.
- The gitleaks and `govulncheck` checks stay in the `go` job on purpose. A
  separate Go job would race the `go` job on the same `setup-go` cache key:
  whichever finishes first saves a tools-only cache and the other's build
  cache is silently discarded, so `go test -race` would pay the cold-cache
  cost after every `go.sum` change. One job means one cache writer.
- Add `astral-sh/setup-uv`, pinned by commit SHA, with a pinned release
  checksum, the uv cache and managed Python cached, and the pinned Python
  3.15.2 installed once so `make test-python` takes its intended uv path
  instead of the Docker-image fallback.
- The Docker fallback remains available as `make test-python-docker`, but it
  is no longer exercised by PR CI. `deploy/images/Dockerfile.intake` is still
  built by the candidate-image workflow (`images.yml`, manual dispatch), which
  does not run the Python tests. This is a deliberate coverage trade for
  speed: per-PR Python tests now run against the same locked dependency graph
  via uv, and the release-image build keeps its existing coverage.
- Keep the `check` gate job name (the only required status check on `master`)
  and make it require every new job.
- Add `concurrency` so a new push to a pull request cancels the superseded
  run. Push runs use `github.run_id` in the group, so no `master` run can
  cancel, replace, or queue behind another.
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
4. Superseded pull-request runs cancel; `master` push runs are never
   cancelled or replaced.

## Validation

All runs below are on draft PR #54 and ended with a green `check` gate.

| Run | Workflow under test | Wall time | Slowest job |
| --- | --- | --- | --- |
| [40642262249](https://github.com/mitkox/esf/actions/runs/40642262249) | baseline, serial | 2m54s | Linux 2m44s |
| [40644784628 attempt 1](https://github.com/mitkox/esf/actions/runs/40644784628) | parallel split, first run | 1m38s | Python 87s (Docker fallback, cache cold) |
| 40644784628 attempt 2 | parallel split, warm cache | 1m23s | Python 72s (Docker fallback) |
| [40645297784](https://github.com/mitkox/esf/actions/runs/40645297784) | + pinned Python 3.15.2 for uv | 1m43s | macOS 91s |
| [40645594342](https://github.com/mitkox/esf/actions/runs/40645594342) | after cancellation test | 72s job execution (2m05s window, 52s of it waiting for the cancelled run's slot) | macOS 64s |
| [40646108314](https://github.com/mitkox/esf/actions/runs/40646108314) | doc update | 93s job execution (97s window) | macOS 86s |

Observations:

- Six jobs (five Linux plus macOS) started together on the first parallel run,
  `check` gated them, and the serial Linux job's 2m44s became a 30-50s
  critical path.
- The rerun exposed the real Python cost: `make test-python` was building the
  intake image (59s) because `uv python find 3.15.2` failed. Installing the
  pinned interpreter dropped the whole Python job from 87s to 24-30s
  (`make test-python` now 12s).
- With Python and Go off the tail, the unchanged macOS job is now the critical
  path (64-91s, run-to-run hosted variance included), so end-to-end time
  approaches the macOS floor. Best observed job execution was 72s against a
  2m54s baseline; the worst observed run window after a cancellation was
  2m05s, still 28% below baseline.
- Per-job durations are the stable comparison because GitHub-hosted scheduling
  varies: Linux critical path went 2m44s -> ~50s, Python 66s -> 12s.
- Superseded-run cancellation is verified: two pushes a few seconds apart left
  the first run `completed/cancelled` and queued the second. Only
  `pull_request` runs have `cancel-in-progress` enabled; push runs are also
  isolated by `run_id`.
- Independent review findings were addressed: the duplicate `setup-go` cache
  writer, the pending-master-run concurrency edge case, the uv checksum
  reliance on a mutable manifest, the stale uv cache key, and the doc accuracy
  issues above.

## Rollback

Revert the workflow-affecting commits (or restore the previous serial
`ci.yml`); nothing else in the repository or its settings changes. The uv
caches created by this workflow are harmless to the old workflow and expire on
their own.

## Cube verification

Not applicable: this change only reorders GitHub Actions jobs. It does not
modify factory, CubeSandbox, Temporal, or runtime behavior, so no Cube
acceptance run is added here. CI still builds the factory and runs the Go,
frontend, Pi, and Python suites that exercise that code.
