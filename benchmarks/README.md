# Harness benchmarks

`harness-compare.sh` measures the factory + harness path without inventing a
new framework:

* **Offline (default):** `go test ./internal/agentharness/...`,
  `go test ./internal/factory/...`, `make build`, `make-e2e-repo.sh`, and the
  unreal overlay validation (`SKIP_SMOKE=1 DRY_RUN=1 scripts/unreal-demo.sh`
  when `UNREAL_HARNESS_LLM_*` is set). No Cube, Temporal, or LLM quota needed.
* **Live (`--live`):** starts `factory worker`, runs `noop` and `conformance`
  through the disposable fixture, reports wall-clock +
  `factory_result`/`agent_result` per run, then runs the `sandboxes` leak
  check. When the Unreal environment is configured it then runs
  `scripts/unreal-demo.sh` with its isolated overlay.

```sh
./benchmarks/harness-compare.sh
./benchmarks/harness-compare.sh --live
```

No module dependency is added: the Unreal adapter speaks the standalone
runner's CLI/JSON contract (see `docs/harness-unreal.md`).
