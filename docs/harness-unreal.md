# unreal-agent harness

ESF integrates the async-first `unreal-agent` runner through a dedicated
`type = "unreal"` adapter. The adapter depends only on the standalone runner's
CLI and JSON protocol; `go.mod` does not import the unreal-agent module.

## Runtime contract

The factory stages the operator-pinned binary at
`/usr/local/bin/unreal-agent-runner` and invokes it directly. There is no shell
wrapper, Python encoder, or task text in argv. ESF writes a JSON request to a
sandbox file and redirects stdin from that file.

The adapter supplies:

- the selected model and validated reasoning level;
- a deterministic session ID derived from the factory run ID;
- a deterministic external message ID derived from the run ID and task;
- the configured provider and gateway URL; and
- either a CubeEgress-managed placeholder or, in legacy mode, the value of
  `api_key_env`, renamed to `UNREAL_HARNESS_LLM_API_KEY`.

Stable IDs let the runner recognize a repeated request while its sandbox-local
session exists. ESF still does not automatically retry an ambiguously completed
agent process: the original process may still be running, and concurrent writers
to one session would be unsafe. Temporal heartbeats, the agent timeout, the
workflow deadline, and sandbox cleanup bound the invocation.

The runner's operation manager remains asynchronous internally. From the
factory's perspective it is one cancellable, heartbeat-backed activity whose
exit code is separate from deterministic verification. Agent success is never
factory success.

## Configuration

```toml
[harnesses.unreal]
type           = "unreal"
binary         = "/opt/factory/bin/unreal-agent-runner"
binary_sha256  = "<64-character-sha256>"
provider       = "openrouter"
base_url       = "https://openrouter.ai/api/v1"
model          = "<provider-model-id>"
api_key_file   = "/run/credentials/factory-worker.service/openrouter-key"
credential_mode = "cube_egress"
thinking_level = "high"
timeout        = "30m"
```

Supported providers are `openai`, `openrouter`, `fireworks`, and `ollama`.
Reasoning levels are `low`, `medium`, `high`, `xhigh`, and `max`. `api_key_env`
is required except for `ollama`.

The configured model is pinned operator policy. A per-run `--model` override is
accepted only when it names that same model; a different model is rejected
before a sandbox is created.

`binary_sha256` is mandatory. ESF hashes the host binary before staging it and
fails provisioning on a mismatch; the actual staged digest is also recorded as
the harness version in `run.json`.

For local development, an environment source is also supported:

```sh
export OPENROUTER_API_KEY='<secret>'
```

and set `api_key_env = "OPENROUTER_API_KEY"` instead of `api_key_file`.
Production configuration must choose exactly one source. Credential files must
be absolute, regular, no larger than 2048 bytes, and inaccessible to group and
other users. This works with systemd credentials and Vault Agent-rendered files.

The source path or variable name is configuration. In `cube_egress` mode its value is resolved
inside the worker activity after repository preparation and sent directly to
Cube's runtime network-policy API. Temporal receives only the harness name and
sandbox ID. CubeEgress overwrites the runner's placeholder `Authorization`
header on matching HTTPS `POST` requests, and unmatched runtime egress is denied
unless explicitly listed in `sandbox.runtime_allow_out`.

This mode requires Cube 0.7 or newer, a template built with the CubeEgress CA,
and an HTTPS `base_url` without userinfo, query, or fragment. The rule pins
scheme, SNI, Host, method, port, and the base-path prefix. `environment` remains
available for compatibility, but it deliberately makes the real key readable
inside the microVM and should not be used for production workloads.
Because the injection policy itself contains the secret, a remote Cube control
plane must use HTTPS; plaintext is accepted only for a loopback endpoint.

`pass_env`, `executable`, `args`, and `prompt_mode` are deliberately not part of
the Unreal configuration. This prevents an operator typo from restoring the
shell adapter or passing unrelated worker secrets.

## Operator runbook

The repository includes a disposable acceptance path that writes an overlay
under `.factory/` and does not modify `factory.toml`:

```sh
export UNREAL_HARNESS_LLM_PROVIDER=openrouter
export UNREAL_HARNESS_LLM_BASE_URL=https://openrouter.ai/api/v1
export UNREAL_HARNESS_LLM_MODEL=<model-id>
export OPENROUTER_API_KEY=<secret>

./scripts/unreal-demo.sh
DRY_RUN=1 ./scripts/unreal-demo.sh
SKIP_SMOKE=1 DRY_RUN=1 ./scripts/unreal-demo.sh  # offline; no key required
```

The demo builds ESF, verifies the runner/configuration, starts a worker, runs a
task through Temporal and CubeSandbox, checks the deterministic gates, and
verifies sandbox cleanup.

## Evidence and security properties

- `agent/stdout.log` contains the runner's JSONL session audit.
- `agent/stderr.log`, `agent/result.json`, and the original `agent/prompt.txt`
  are stored with the normal factory evidence bundle.
- `run.json` records the model and a SHA-256 digest of the staged runner binary.
- Runner session and operation files live under
  `/workspace/.factory/unreal/` while the sandbox is alive. The durable audit is
  stdout, captured and redacted before sandbox destruction.
- Provider, URL, model, reasoning level, environment name, package name, and
  dynamic-placeholder validation happens at worker startup.
- The task and model are JSON values, never shell source. The generic harness
  also rejects dynamic placeholders embedded in shell programs.
- `cube_egress` applies a provider-only, deny-by-default policy immediately
  before agent execution. Extra runtime destinations must be explicitly listed.
- CubeEgress records metadata-only allow, deny, and injection decisions in its
  host audit log; secret values are not included.
