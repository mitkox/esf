# Claude Code and Codex CLI harnesses

ESF supports `type = "claude"` and `type = "codex"` as opt-in agent
harnesses. OpenCode remains the automatic default. Select a configured name
with `factory run ... --agent claude` or `--agent codex`; the same names work
with `factory exec` and the factory API.

Both CLIs execute inside a disposable Cube microVM. ESF delivers the task on
stdin, records JSON output and the CLI version, applies its normal deterministic
verification and review gates, collects the patch, and destroys the sandbox.
The CLI's completion event never overrides failed verification. A zero exit
without a successful terminal JSON event also fails the agent step.

Install an operator-approved **standalone Linux binary** at
`/opt/esf/agents/claude` or `/opt/esf/agents/codex`. A Node/npm launcher cannot
be copied by itself; put it and its dependencies in a versioned Cube template
instead. ESF does not download or update either CLI during a run. Pin the
binary digest and qualify the template with the chosen CLI version.

These factory harnesses require `credential_mode = "cube_egress"`. ESF resolves
an owner-only credential file or a named worker environment variable, then
sends the value to the trusted Cube control plane for HTTPS header injection.
The VM receives only a placeholder. Real provider keys are excluded from its
environment, argv, configuration files, Temporal payloads, and stored evidence.
Host environment passthrough and host login/session files are rejected.

Development can stage a digest-pinned standalone binary from the worker host.
Production should preinstall the complete runtime in a qualified Cube template.
Both use the same credential boundary.

```toml
[harnesses.claude]
type = "claude"
binary = "/opt/esf/agents/claude"
model = "YOUR_APPROVED_CLAUDE_MODEL"
timeout = "30m"
api_key_env = "ANTHROPIC_API_KEY" # worker only
credential_mode = "cube_egress"
base_url = "https://api.anthropic.com"

[harnesses.codex]
type = "codex"
binary = "/opt/esf/agents/codex"
model = "YOUR_APPROVED_CODEX_MODEL"
timeout = "30m"
api_key_env = "OPENAI_API_KEY" # worker only
credential_mode = "cube_egress"
base_url = "https://api.openai.com/v1"
```

An omitted model lets the installed CLI choose its default. Set an explicit
model for reproducible evidence. `base_url` supports an Anthropic Messages
endpoint for Claude and an OpenAI Responses endpoint for Codex; a gateway that
only implements Chat Completions is insufficient for Codex. Named model
resources can override the model. Their endpoint and credential source must
match the harness CubeEgress configuration. Their provider must be `anthropic`
or `openai`, respectively.

For production, preinstall the complete runtime in the Cube template and add
these fields to each harness. Replace the digest with the actual binary SHA-256:

```toml
preinstalled = true
binary_sha256 = "REPLACE_WITH_64_HEX_DIGITS"
credential_mode = "cube_egress"
base_url = "https://api.anthropic.com" # Codex: https://api.openai.com/v1
# Use exactly one credential source. Remove api_key_env when using a file.
api_key_file = "/run/credentials/factory-worker.service/provider-key"
```

CubeEgress holds the real credential and injects `x-api-key` for Claude or
`Authorization: Bearer ...` for Codex into HTTPS POST requests to the inference
endpoints only. Claude permits
`/v1/messages` and `/v1/messages/count_tokens`; Codex permits `/responses` and
`/responses/compact` under its configured API base path. Account/admin endpoints
do not receive credential injection. The CLI receives a placeholder. A named model endpoint must match the configured
CubeEgress endpoint and credential source. Custom argv, environment
passthrough, and host login/session files are rejected for these harness types. Interactive subscription login and
host authentication files are not provisioned by these adapters.

Claude uses print mode with streaming JSON, disables ambient settings and MCP
configuration, marks its process as sandboxed with `IS_SANDBOX=1`, and approves
tools inside the Cube VM. Codex uses `exec --json`, stdin prompt input, a private `CODEX_HOME`, and an explicitly configured
Responses provider. Their CLI permission bypass flags are confined to Cube;
ESF's verification, review, and network policy remain authoritative.

The CLI contracts are documented in the
[Claude Code CLI reference](https://code.claude.com/docs/en/cli-reference) and
[official Codex CLI reference](https://developers.openai.com/codex/cli/reference).
Adapter/conformance checks and CLI startup checks are distinct from successful
model-backed acceptance and production qualification.

## OpenCode Go with DeepSeek

The 0.7.0 local Linux/amd64 profile qualified Claude Code 2.1.300 and Codex
0.162.1 with `deepseek-v4.1-flash` through OpenCode Go. The 0.7.1 candidate
pins Claude Code 2.1.312 and Codex 0.165.0 and verifies both binary digests and
CLI startup inside the rebuilt Cube template; model-backed acceptance for the
refreshed pins remains a separate gate. The names `claude` and `codex` select
the CLI harness; inference uses the selected DeepSeek model. Both run inside
Cube, with the Go key injected by CubeEgress. This uses the Go API key, not a
copied interactive Claude or Codex login session.

For preinstalled, digest-pinned CLIs, replace the credential path below with
an owner-only worker file and set each binary's measured SHA-256:

```toml
[sandbox]
# Must match the template's DNS resolver; retain other approved runtime targets.
runtime_allow_out = ["1.1.1.1"]

[harnesses.claude]
type = "claude"
binary = "/opt/esf/agents/claude"
preinstalled = true
binary_sha256 = "REPLACE_WITH_64_HEX_DIGITS"
model = "deepseek-v4.1-flash"
base_url = "https://opencode.ai/inference/go"
credential_mode = "cube_egress"
api_key_file = "/run/credentials/factory-worker.service/opencode-go"

[harnesses.codex]
type = "codex"
binary = "/opt/esf/agents/codex"
preinstalled = true
binary_sha256 = "REPLACE_WITH_64_HEX_DIGITS"
model = "deepseek-v4.1-flash"
base_url = "https://opencode.ai/inference/go/v1"
credential_mode = "cube_egress"
api_key_file = "/run/credentials/factory-worker.service/opencode-go"
```

Remove any `api_key_env` entry when selecting the file source. DNS destinations
in Cube's `allow_out` use IPs or domains, not `IP:port` strings. The factory
replaces the runtime policy with deny-by-default, protocol-specific POST rules.
Preserve the clients' native session headers through the proxy, as described
in the [OpenCode Go documentation](https://opencode.ai/v2/docs/console/go/).

The [qualification record](../release/qualification/live-upgrade-2026-10-15.md)
retains verified patches, expected verification failures, review pause/resume,
live credential-boundary checks and proxy denials. Cube hosts sharing Calico
or marked VPN routing may also need the
[operator routing configuration](cube-local-routing.md). These results qualify
the recorded local profile; broader production gates remain separate.
