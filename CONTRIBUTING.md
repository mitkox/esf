# Contributing

ESF builds on Machinist and uses Go for the CLI, control plane, and workers, plus React and Vite
for the embedded browser interface.

Before starting a change, search existing issues and keep the scope focused. Do
not include credentials, private repository data, or sensitive task content.
Report vulnerabilities through [SECURITY.md](SECURITY.md), not a public issue.

## Setup

Install:

- Go 1.26.6
- Node.js 22.22.2 or a compatible newer release, plus npm
- Git
- `just` for repository shortcuts
- the agent CLI needed for any real execution checks

Build the factory with `go build -o bin/factory ./cmd/factory`.
Build the inherited Machinist CLI and its embedded frontend:

```sh
just build
```

The binary is written to `bin/machinist`.

## Checks

Run the complete project check before opening a pull request:

```sh
just check
```

CI separately proves that Go formatting is current without changing files,
runs `go vet`, runs the Go suite with the race detector on Linux and macOS,
tests and builds the frontend, confirms the tracked frontend bundle is current,
and builds the `factory` and `machinist` executables.

The frontend bundle under `internal/controlplane/web/dist` is committed because
it is embedded into the Go binary. If frontend source changes, rebuild and
commit the generated assets:

```sh
cd internal/controlplane/web
npm ci
npm test
npm run build
```

## Dependency updates

Dependabot checks Go and npm dependencies each week and GitHub Actions each
month. Review upstream release notes and keep each manifest and lockfile in sync.

Workflow actions must use a full commit SHA followed by a version comment, for
example `owner/action@0123456789abcdef0123456789abcdef01234567 # v1.2.3`.
Do not replace a pinned SHA with a mutable tag.

## Pull requests

- Keep changes focused.
- Include tests for changed behavior.
- Update `ARCHITECTURE.md` when current boundaries or contracts change.
- Use Conventional Commit messages.
- Explain what was verified, including browser checks for UI work.
- Do not commit credentials, local configuration, databases, or run artifacts.

Changes to `main` must go through a pull request and pass the required `check`
status. By participating, you agree to follow the
[Code of Conduct](CODE_OF_CONDUCT.md). Contributions use the project's
[MIT License](LICENSE).
