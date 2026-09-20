# ESF — Engineering Software Factory

[![CI](https://github.com/mitkox/esf/actions/workflows/ci.yml/badge.svg)](https://github.com/mitkox/esf/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A self-hosted software factory that runs coding agents in isolated CubeSandbox
microVMs, verifies their changes, and preserves the patch and execution evidence.
Temporal coordinates the workflow and sandbox cleanup.

```text
Task → Temporal → CubeSandbox → coding agent → verification → patch + evidence
```

ESF is an independent fork of [Machinist](https://github.com/owainlewis/machinist)
by Owain Lewis. It retains the Machinist CLI, local control plane, and web UI,
and adds factory orchestration. See [upstream attribution](docs/upstream.md).

## Features

- Named, operator-configured agent harnesses and repository policies.
- Isolated execution in an existing CubeSandbox deployment.
- Deterministic verification gates; agent success alone is not factory success.
- Durable patches, logs, manifests, and verified cleanup outcomes.
- Temporal TLS/mTLS, API-key authentication, and optional encrypted payloads.
- Bounded execution, cancellation, and recovery after a lost create response.

This is actively developed software. Review the [validated behavior and
operational requirements](docs/production-readiness.md) before deployment.
ESF produces changes for review; it does not decide what ships.

## Quick start

Requirements: Go 1.26.6 or newer, Git, an existing CubeSandbox deployment with a
READY template, and Temporal. Docker Compose can run the included local Temporal
stack. Node.js 22.22.2 or newer is needed for frontend development.

```sh
git clone https://github.com/mitkox/esf.git
cd esf
mkdir -p bin
go build -trimpath -o bin/factory ./cmd/factory
cp factory.example.toml factory.toml
```

Edit `factory.toml` with your Cube API endpoint, template, proxy address, agent
harness and verification profile. Configure narrowly scoped credentials locally.
Neither `factory.toml` nor `.env` belongs in Git.

For local Temporal:

```sh
cp deployments/dev/temporal/.env.example deployments/dev/temporal/.env
# Set a random database password in that .env file before starting.
make temporal-up
./bin/factory doctor
./bin/factory worker
```

In another terminal, submit a task for an allowed repository:

```sh
./bin/factory run \
  --repo https://github.com/your-org/your-repo \
  --rev FULL_COMMIT_SHA \
  --task "Describe the change and acceptance criteria" \
  --agent opencode2 \
  --verification default
```

The default profile expects repository-owned `build.sh` and `test.sh` scripts.
Configure gates appropriate to your project. Inspect results with
`factory status RUN_ID` and `factory logs RUN_ID`:

```sh
./bin/factory describe run RUN_ID       # manifest + conditions + inventory + audit
./bin/factory status RUN_ID --watch     # stream condition transitions
./bin/factory get changes               # durable work items and their spend
```

With `[review] enabled = true`, a run pauses after the gates report:

```sh
./bin/factory review RUN_ID --approve
./bin/factory review RUN_ID --reject --note "use the formal greeting"
./bin/factory run --change CHANGE_ID --parent-run RUN_ID ...   # rework activation
```

A run can also be submitted from a reviewed manifest:

```sh
./bin/factory apply -f run.toml
```

Build the inherited CLI separately with
`go build -trimpath -o bin/machinist ./cmd/machinist`, then run
`./bin/machinist init`.

## Documentation

| Guide | Purpose |
| --- | --- |
| [Factory overview](docs/FACTORY.md) | Workflow and components |
| [Operator guide](docs/operator-guide.md) | Harnesses, verification and troubleshooting |
| [Production deployment](docs/production-deployment.md) | Service setup, TLS, encryption and recovery |
| [Readiness review](docs/production-readiness.md) | Validation and operational requirements |
| [Machinist documentation](docs/README.md) | Inherited CLI and control plane |
| [Architecture decisions](docs/adr/) | Design rationale |

## Development

```sh
go test -race ./...
go vet ./...
cd internal/controlplane/web
npm ci
npm test
npm run build
```

Integration tests require configured services; see the operator guide.
Read [CONTRIBUTING.md](CONTRIBUTING.md) and the [Code of Conduct](CODE_OF_CONDUCT.md).
Report vulnerabilities privately through [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE). Original Machinist copyright and attribution are preserved.
