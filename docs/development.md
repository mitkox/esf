# Development

## Requirements

- Go 1.27.1 and Node.js 24.21.0, pinned in `.mise.toml`
- Python 3.14.8 for the optional intake and brief lab tests
- `just` for Machinist commands and `make` for factory commands

Run `mise install` to install the pinned local toolchains. CI checks the same
Go and Node versions. Python tests can also run in the pinned intake image via
`make test-python-docker`.

## Build

Build the React application, embed it, and compile Machinist:

```sh
just build
```

The binary is written to `bin/machinist`.

For backend-only changes that do not touch the frontend, the tracked production
assets allow a direct Go build:

```sh
go build ./...
```

## Run locally

Start the control plane and managed worker together:

```sh
just local
```

The control plane uses the repository's `examples/config.toml`. The managed
worker continues to use `~/.machinist/worker.toml` because executors,
credentials, and repository paths are machine-owned configuration.

## Verify

Run the complete project check before opening a pull request:

```sh
just check
```

This installs the locked frontend dependencies, runs frontend tests, rebuilds
the embedded assets, runs Python eval tests, checks and vets the Go code, runs
Go tests with the race detector, and builds all Go packages.

Focused commands are also available:

```sh
just test
python3 -m unittest discover -s evals -p 'test_*.py'
cd internal/controlplane/web && npm test
go test ./internal/runner
```

## Project layout

```text
cmd/machinist/                CLI entry point
examples/                     embedded default configuration and prompts
internal/cli/                 command behavior
internal/config/              strict TOML loading and template resolution
internal/runner/              process execution and event recording
internal/controlplane/        HTTP server, SQLite store, and embedded UI
internal/managedworker/       polling, leases, execution, and result delivery
docs/                         user and design documentation
```

The frontend source lives in `internal/controlplane/web/src`. Its production
bundle lives in `internal/controlplane/web/dist` because Go embeds those files at
compile time.

For local Cube callbacks, see the [routing and narrow firewall procedure](cube-local-routing.md).
