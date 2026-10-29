# Upstream history and attribution

ESF began as an independent fork of
[Machinist](https://github.com/owainlewis/machinist), originally authored by
Owain Lewis under the MIT License. Factory development began from upstream
commit `7b08de0` on `main` and added Temporal orchestration, CubeSandbox
execution, agent harnesses, deterministic verification and durable evidence.
Initial ESF publication was a sanitized source snapshot, excluding local
credentials, deployment discovery records and execution reports.

The inherited Machinist CLI, local control plane, SQLite store, managed worker
and web UI were **removed in ESF 0.6.2**. The `machinist` command, its
`config.toml`/`worker.toml` formats, `~/.machinist` state and the console
archives/images are no longer part of this project. The decision and rationale
are recorded in [ADR 0008](adr/0008-remove-machinist.md).

The Go module remains `github.com/mitkox/esf`; the only command is `factory`.
ESF is maintained at [mitkox/esf](https://github.com/mitkox/esf), independently
of upstream releases, website and security reporting. The original copyright
and MIT license terms are retained in [LICENSE](../LICENSE) for the historical
contribution; no inherited Machinist source remains in the current tree.

The execution provider uses the [Tencent CubeSandbox SDK](https://github.com/tencentcloud/CubeSandbox).
ESF consumes an operator-managed deployment and does not install or alter Cube
components or templates. Dependencies are pinned in [go.mod](../go.mod).

Attribution for adapted Temporal configuration is in its
[README](../deployments/dev/temporal/README.md). Third-party Go, npm and Python
components retain their respective licenses; the generated release SBOM records
the dependency graph.
