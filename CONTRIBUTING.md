# Contributing

ESF is a self-hosted software factory written in Go. The factory CLI, Temporal
workflow, CubeSandbox execution and verification gates are the product surface;
there is no browser interface or separate control-plane service.

Before starting a change, search existing issues and keep the scope focused. Do
not include credentials, private repository data, or sensitive task content.
Report vulnerabilities through [SECURITY.md](SECURITY.md), not a public issue.

## Setup

Install:

- Go 1.27.3
- Node.js 26.12.1, plus npm, for the optional Pi runner and agent packaging
- Python 3.15.2 and uv 0.13.6 for optional Python checks
- Git
- the agent CLI needed for any real execution checks

Build the factory with `go build -o bin/factory ./cmd/factory` or `make build`.

## Checks

Run the project checks before opening a pull request:

```sh
make lint
make test
go test -race ./...
```

CI separately proves that Go formatting is current without changing files,
runs `go vet`, runs the Go suite with the race detector on Linux and macOS,
validates the frozen release inventory and SBOM, runs the release-qualification
and script tests, and builds the factory executable.

## Dependency updates

Dependabot checks Go, agent npm packages and Python dependencies on a schedule,
and GitHub Actions monthly. Review upstream release notes and keep each
manifest and lockfile in sync with `release/inventory.json`; run
`python3 scripts/verify-release-inventory.py` after changing a pin.

Workflow actions must use a full commit SHA followed by a version comment, for
example `owner/action@0123456789abcdef0123456789abcdef01234567 # v1.2.3`.
Do not replace a pinned SHA with a mutable tag.

## Pull requests

- Keep changes focused.
- Include tests for changed behavior.
- Update `ARCHITECTURE.md` when current boundaries or contracts change.
- Use Conventional Commit messages.
- Explain what was verified.
- Do not commit credentials, local configuration, databases, or run artifacts.

Changes to `master` must go through a pull request and pass the required `check`
status. By participating, you agree to follow the
[Code of Conduct](CODE_OF_CONDUCT.md). Contributions use the project's
[MIT License](LICENSE).
