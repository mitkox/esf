# Pi harness v1 candidate

Select Pi explicitly with `factory run --agent pi`. OpenCode remains the
automatic default. Pi 1.0.4 has an experimental API; this adapter pins its
published packages, dependency graph, ESF bundle, and Node 24.21.0 runtime.
The candidate is opt-in until the model-backed acceptance matrix passes.

## Template and installation

`agents/pi/inventory.json` describes the candidate separately from historical
release records. `deploy/cube/Dockerfile.pi-v1` extends a qualified ESF template
selected by immutable image digest:

```sh
docker build -f deploy/cube/Dockerfile.pi-v1 \
  --build-arg ESF_TEMPLATE_BASE=<registry/image@sha256:digest> \
  -t <registry/esf-pi-template:candidate-v1> .
```

Publish the candidate image to the operator's registry, then create a new Cube
template from its immutable digest using the existing template runbook.
Record the READY template ID in a development overlay. Keep the existing
`factory.toml` and default template intact. No npm installation occurs in a
factory run. For offline template staging,
`scripts/install-pi-agent.sh <pinned-node-binary> <staging-agent-directory>`
installs the verified Node binary and committed bundle.

Rebuild the bundle and catalog in a Cube development microVM, using Node
24.21.0 and the committed lockfile:

```sh
cd agents/pi
npm ci --ignore-scripts
npm run build
node export-catalog.mjs
npm test
npm audit --audit-level=high
```

Refresh the candidate inventory and template hashes when the bundle changes.
`scripts/verify-pi-inventory.py` checks the lock, upstream versions, bundle,
and template pins. `scripts/generate-sbom.py` includes the complete locked Pi
npm graph, the bundle digest, and Node runtime. Keep existing release SBOMs
and inventories unchanged.
The reproducible build collects license notices for the bundled modules and
Node into `dist/THIRD_PARTY_NOTICES.txt`; the template and offline installer
retain that file beside the execution chain.

## Configuration

Copy the operator configuration to a private overlay, select the new READY
template in `[cube]`, and append:

```toml
[harnesses.pi]
type = "pi"
preinstalled = true
binary = "/opt/esf/agents/pi-runner.mjs"
binary_sha256 = "018aec9e0215eff09aede13a2b707f6e6872d1dcf2b44727d5afc3d6e97c42fc"
runtime_binary = "/opt/esf/agents/node"
runtime_sha256 = "7fde7b8afa198da66257f42ee2001d874c7355631e6d1579a5fb5ef1f246df4c"
provider = "local"
base_url = "http://192.168.0.14:8000/v1"
model = "<operator-approved-model-id>"
api = "openai-completions"
catalog_cache = "/absolute/path/to/validated-model.json"
thinking_level = "off"
max_process_restarts = 1
timeout = "30m"
```

`api` supports `openai-completions` (default for the local gateway) and
`openai-responses`. A model in the embedded Pi 1.0.4 catalog can omit
`catalog_cache`; its provider, model ID, and API must match. Custom gateways
must supply a strict offline JSON model definition with explicit context and
output limits. Do not guess these limits from a model name.
`agents/pi/models/deterministic.json` defines only the test provider.

An offline model definition declares `id`, `name`, `api`, `reasoning`,
`input: ["text"]`, `contextWindow`, `maxTokens`, `thinkingLevels`, and
`pricingKnown`. Optional `cost` rates (`input`, `output`, `cacheRead`,
`cacheWrite`) are USD per million tokens. Optional `compat` is Pi's
OpenAI API compatibility object. `thinkingLevelMap` maps approved Pi levels
to provider-specific values; `xhigh` and `max` require explicit mappings.
Unknown fields, missing limits, API/model mismatches, and unsupported reasoning
fail startup. Named model resources must agree with the harness's provider,
model, endpoint, and credential source before a sandbox is allocated.

For hosted providers use an HTTPS hostname and `credential_mode = "cube_egress"`,
with exactly one `api_key_file` (absolute, regular, owner-only) or `api_key_env`.
The key is sent only to Cube's scoped injection policy. Pi receives
`ESF_PI_API_KEY=cube-egress-managed-placeholder`, and Node uses the template's
system CA trust in both supervisor and engine. The same deny-by-default policy
and endpoint checks apply as for Unreal and OpenCode. Legacy
`credential_mode = "environment"` exposes only the configured key under the
canonical name inside the VM and is for development. Pi accepts no `pass_env`,
custom executable/argv, provider files, packages, or extensions.

Run `factory --config <overlay> config validate`, `factory --config <overlay>
agents verify`, then start a worker with that overlay and an isolated task
queue. Submit through the same overlay using `--agent pi` and the existing
verification/review flags. Check `factory sandboxes` after every acceptance run.

## Recovery boundary

The fixed invocation reads one versioned JSON request via `stdin_file`. It
contains the run/task, repository, approved model settings, deadline, and
execution-chain hashes. A stable request ID and configuration fingerprint bind
the submission to `/workspace/.factory/pi/<sha256(run-id)>`. Pi retains its
own conversation and session IDs in its fsync-enabled JSONL journal.

One supervisor owns an exclusive session lock and the original deadline across
engine restarts. It permits at most one restart after unexpected engine death
without a terminal notification. Normal model/storage errors, cancellation,
and timeout fail without restart. A shell tool must receive an acknowledged
supervisor notification before spawning. Engine death with an active shell
tool fails the run. Additional surviving processes or uncertain process
inspection also prevent recovery. No process-containment infrastructure is
added; factory teardown destroys the entire VM.

Recovery reopens the same journal and reacquires the same submission. All
coding tools retain unsafe replay policy and run sequentially. An interrupted
file tool is recorded as interrupted; another model turn can inspect and act
on the current tree. This does not guarantee exactly-once effects. Missing,
corrupt, mismatched state, lost acknowledgements, or supervisor death remain
failed runs. Temporal retains its existing single-attempt agent policy.
Journal and sidecars remain untrusted agent-writable scratch, never factory
verification or approval authority.

## Evidence and acceptance

The standard stdout artifact contains commit-derived JSONL audit events,
including a final committed submission receipt. Standard stderr/result and
prompt artifacts remain unchanged. Before teardown, the adapter validates and
the factory redacts/stores `agent/pi-terminal.json`, `agent/usage.json`, and
`agent/recovery.json`. Each sidecar is bounded to 128 KiB. Exit zero without a
valid matching terminal receipt fails. Sidecars are transient harness output;
they do not add workflow payloads or manifest fields. Factory gates, blocked
signals, behavior monitoring, review suspend/resume, and cleanup remain in the
existing lifecycle.

Input tokens sum `input + cacheRead + cacheWrite`; output already includes
reasoning tokens. Committed usage is aggregated once from the same ledger
across restarts. Missing metering or interrupted requests makes totals
unavailable and identifies retained measurements as a known floor. Unknown or
tiered prices make cost unavailable. Known flat rates produce an explicitly
estimated cost, never invoice evidence.

Deterministic provider tests exercise both APIs, deduplication, owner locking,
generation/file-tool recovery, final-commit recovery, unsafe shell refusal,
surviving processes, uncertain inspection, corrupt/missing state, cancellation,
original deadline, and exhausted allowance. Go suites cover digest rejection,
fixed prompt transport, model policy, proxy credentials, and artifact redaction.
Local validation runs these suites inside Cube microVMs.

Promotion requires model-backed factory runs on the local gateway and an HTTPS
CubeEgress profile: verified completion, verification failure, review
suspend/resume, audit/usage retention, and zero leaked sandboxes. Compare the
same task/model against OpenCode and Unreal, recording completion, latency,
tokens, and recovery overhead. Cost comparisons require known pricing.

The 2026-10-05 local candidate passed 16 runner regressions, the existing
agentharness/factory/CLI Go suites with the race detector, and deterministic
factory completion, verification-failure, blocked-task, and review
suspend/resume scenarios inside Cube. All test VMs were destroyed. The pinned
npm graph reported zero audit vulnerabilities. Real-model acceptance and
harness comparisons remain pending: the configured local gateway was
unreachable, and no HTTPS provider/model/credential profile was available.
These results qualify the deterministic integration only; the inventory
continues to mark Pi as a candidate.

The 2026-10-06 upgrade to Pi Durable 1.0.4 passed 17 runner regressions and
model-backed factory completion on the local DeepSeek gateway. OpenCode 2.0.24
and Unreal 0.3.1 passed the same task/model comparison, including independent
verification and sandbox cleanup. Pi remains an opt-in candidate pending the
HTTPS CubeEgress, failure, review, and recovery acceptance matrix described
above. See [the component upgrade evidence](../release/qualification/component-upgrade-2026-10-06.md).
