# Development

## Requirements

- Go 1.27.3, pinned in `.mise.toml`
- Node.js 26.12.1 for the optional Pi runner and agent packaging
- Python 3.15.2 for the optional intake and brief lab tests
- CubeSandbox and Temporal only for integration or live execution checks

Run `mise install` to install the pinned local toolchains. CI checks the same
Go and Node versions. Python tests can also run in the pinned intake image via
`make test-python-docker`.

## Build

```sh
make build
```

The factory is written to `bin/factory`; the discovery tools (`cube-smoke`,
`cube-netprobe`, `cube-agent-spike`) are built beside it. A direct Go build is
also sufficient for most changes:

```sh
go build ./...
```

## Run locally

```sh
cp deployments/dev/temporal/.env.example deployments/dev/temporal/.env
# set a random database password, then:
make temporal-up
./bin/factory init
./bin/factory config validate
./bin/factory doctor
./bin/factory worker
```

Submit a run from another terminal with `./bin/factory run ...`; see the
[operator guide](operator-guide.md) for configuration and examples.

## Verify

Run the project checks before opening a pull request:

```sh
make lint
make test
go test -race ./...
python3 -m unittest discover -s scripts/tests -p 'test_*.py'
```

`make test` runs the Go suite plus the optional Python and Temporal schema
checks. AI and Cube integration suites require configured services:

```sh
make integration-test
```

CI is the authoritative Linux/macOS test run, including the race detector,
`go vet`, formatting, secret scanning, `govulncheck`, the release-inventory
check, SBOM validation, and the Pi runner build.

## Project layout

```text
cmd/factory/                  factory CLI and control plane
cmd/turn-executor/            private sf turn API over the Cube adapter
cmd/turn-smoke/               sf turn smoke tool
internal/factory/             Temporal workflow, activities, config, resources, review gates
internal/factoryartifacts/    durable runs, changes, patches and evidence
internal/agentharness/        operator-registered agent harnesses
internal/sandbox/             execution-provider interface and Cube adapter
internal/verification/        deterministic build and test gates
internal/repository/          exact-revision repository preparation
internal/threatmon/           execution-time behavior monitor
internal/harnessprofile/      Harness2 instructional profiles
internal/inferencebudget/     inference budget accounting
internal/notify/              run alerts
internal/releaseinfo/         embedded release inventory
internal/turnexec/            sf turn execution state and leases
agents/                       optional packaged agent runners
deploy/                       images, Helm chart, systemd worker unit
scripts/                      release, qualification and operator tooling
third_party/                  vendored method/notices (Harness2)
tools/                        Cube discovery and optional Python utilities
docs/                         user and design documentation
```

For local Cube callbacks, see the [routing and narrow firewall procedure](cube-local-routing.md).
