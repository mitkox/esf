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
- Defense in depth at execution time: a measured egress boundary, a behavior
  monitor that quarantines out-of-bounds runs, a documented agent stop signal,
  gate-integrity checking, and `factory cancel` / `halt` / `threats`.

This is actively developed software. Review the [validated behavior and
operational requirements](docs/production-readiness.md) before deployment.
ESF produces changes for review; it does not decide what ships.
The v0.5.0 archives are a binary preview. VM, Kubernetes, container-image,
security, restore, and soak qualifications are recorded separately in the
[release inventory](release/inventory.json) and are not complete.

> **Upgrading:** the factory now refuses to start when an egress policy leaves
> public internet possible unless you set `hardening.acknowledge_open_egress =
> true`. This is deliberate; see [ADR 0007](docs/adr/0007-defense-in-depth.md).
> Run `factory doctor` to see the posture and every warning.

## Quick start

Requirements for v0.5.0 development: Go 1.27.1, Node.js 24.21.0, Git,
an existing CubeSandbox deployment with a READY template, and Temporal.
Python tools use the frozen `uv.lock`; they are optional. Docker Compose can
run the included local Temporal stack.

```sh
git clone https://github.com/mitkox/esf.git
cd esf
mkdir -p bin
make build
./bin/factory init
```

Edit `factory.toml` with your Cube API endpoint, template, proxy address, agent
harness and verification profile. Configure narrowly scoped credentials locally.
Neither `factory.toml` nor `.env` belongs in Git.
The example uses placeholder production endpoints and paths. Set these to
your actual TLS-protected Cube and Temporal services, or loopback development
services, before running `factory config validate`.

The v0.5.0 release installs the factory archive by default. The console and
managed worker use the separate Machinist archive or the optional combined
archive. Install pinned agent binaries separately with
`scripts/install-agents.sh`; `factory agents verify` checks configured digests.

For local Temporal:

```sh
cp deployments/dev/temporal/.env.example deployments/dev/temporal/.env
# Set a random database password in that .env file before starting.
make temporal-up
./bin/factory config validate
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
Machinist now supports staged workflows, review gates, shared artifacts, and
final-message summaries. See [workflow guidance](docs/workflows.md).

## Documentation

| Guide | Purpose |
| --- | --- |
| [Factory overview](docs/FACTORY.md) | Workflow and components |
| [Operator guide](docs/operator-guide.md) | Harnesses, verification and troubleshooting |
| [DSPy/Jev intake](docs/production-deployment.md#optional-dspyjev-intake-advisory) | Optional typed task advice and secure TypeSafe credential setup |
| [DSPy brief lab](tools/brief_lab/README.md) | Offline, evidence-scored implementation brief experiments |
| [Production deployment](docs/production-deployment.md) | Service setup, TLS, encryption and recovery |
| [Readiness review](docs/production-readiness.md) | Validation and operational requirements |
| [Machinist documentation](docs/README.md) | Inherited CLI and control plane |
| [Architecture decisions](docs/adr/) | Design rationale |

## Development

```sh
make lint
make test
make frontend
go test -race ./...
```

Integration tests require configured services; see the operator guide.
Read [CONTRIBUTING.md](CONTRIBUTING.md) and the [Code of Conduct](CODE_OF_CONDUCT.md).
Report vulnerabilities privately through [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE). Original Machinist copyright and attribution are preserved.
