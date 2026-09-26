# OpenCode Go in the ESF factory

[OpenCode Go](https://dev.opencode.ai/docs/go/) offers DeepSeek V4.1 Flash as
`deepseek-v4.1-flash` through its HTTPS Chat Completions endpoint. Use a Go API
key from the OpenCode console. Keep the key on the factory worker host; do not
copy OpenCode's `auth.json`, provider state, or the key into a Cube template.

For a local VM test, create an owner-only key file outside the repository. The
following command prompts without echoing the key or putting it in shell
history:

```sh
umask 077
install -d -m 0700 "$HOME/.local/share/esf/credentials"
systemd-ask-password 'OpenCode Go API key' > "$HOME/.local/share/esf/credentials/opencode-go.key"
```

The production systemd worker can instead use `LoadCredential=` and point
`api_key_file` at its `/run/credentials/` path. A Kubernetes worker can mount a
Secret as a read-only, owner-only file. Neither deployment should put the key
in an environment variable or in the agent's filesystem.

Configure the OpenCode V2 harness with a pinned template binary and the Go
gateway. This example shows only the relevant settings; retain the other
factory, Cube, Temporal, storage, and verification settings from a validated
configuration:

```toml
[sandbox]
runtime_allow_out = []

[intake]
enabled = false

[egress.opencode-go]
allow_internet = false
allow_out = []

[scopes.default]
egress = "opencode-go"

[harnesses.opencode2]
type = "opencode"
binary = "/opt/esf/agents/opencode2"
binary_sha256 = "10d405161d8b9595f4a2ec31254e961969e6ca3f8239ac9bd55b6ff6431dc24f"
preinstalled = true
packages = []
pass_env = []
model = "opencode-go/deepseek-v4.1-flash"
base_url = "https://opencode.ai/zen/go/v1"
credential_mode = "cube_egress"
api_key_file = "/run/credentials/factory-worker.service/opencode-go-key"
catalog_cache = "/var/lib/esf/agents/opencode-models.json"
```

CubeEgress permits only HTTPS POST requests to
`opencode.ai/zen/go/v1/*` with that hostname and TLS SNI. It injects the bearer
key after the sandbox has prepared the repository. The agent receives a
placeholder key, and raw runtime egress remains closed. Set `pass_env = []`
explicitly: the development default otherwise forwards `FACTORY_AGENT_TOKEN`.
Pin and verify the OpenCode model-catalog snapshot at `catalog_cache`; the
worker stages it before agent startup, so a blocked catalog refresh cannot
hold up an isolated run. Keep optional intake disabled for a test that should
use only OpenCode Go. Run `factory config validate`, `factory agents verify`,
and `factory doctor --profile production` before enabling the worker. The last
command also checks production Temporal TLS and other deployment settings.

For a disposable model-backed acceptance run, set `FACTORY_CONFIG` to a
separate private configuration and run
`FACTORY_AGENT=opencode2 bash scripts/factory-run-demo.sh`. Check the run's verification and cleanup
conditions and confirm `factory sandboxes` reports no live sandboxes. Usage
and monetary budgets remain advisory unless the provider enforces them.

Unreal 0.2.0 still uses a `/responses` endpoint in its
[OpenRouter client](https://github.com/unreallabsai/unreal-agent/blob/v0.2.0/harness/llm/clients/openrouter/client.go).
OpenCode Go documents a `/chat/completions` endpoint for this model, so the
same Go profile is for OpenCode V2; Unreal requires a compatible provider or
an operator-controlled adapter.
