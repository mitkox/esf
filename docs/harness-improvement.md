# Controlled Harness2 improvement

ESF implements the Harness2 Propose–Probe–Compose method in Go and Temporal.
The upstream source is pinned in `third_party/harness2/UPSTREAM.json`; its Python
runner, installers and process launchers are not dependencies. Normal factory
runs retain their existing behavior. sf turn execution is outside this feature.
The initial adapter accepts OpenCode2 only. Pi Durable and Unreal Agent continue
to use their existing configuration. Supporting either requires a separately
qualified instructional adapter: Pi recovery/replay and Unreal tool permissions
remain operator-owned. Each needs its own native loading, isolation, recovery,
quality and cost evidence before enrollment.

The implementation is a candidate. The checked-in default enables no policy and
contains no approved profile, paid-provider pricing attestation, or production
quality claim. Local deterministic-provider/native-client tests do not establish
model-backed quality, Kubernetes qualification, or production acceptance.

## Profiles and trust

A profile is an immutable JSON object using `esf-harness-profile/v1`. Its binding
contains `scope`, `repository` (remote URL or `local:/absolute/path`), `harness`
(`opencode2`), `model` (ESF `provider/model`), `harness_digest` (`sha256:...`), and
`template` (exact Cube template ID). It also contains `upstream`, `author`,
`evidence`, optional `parent`, and `components`.

Allowed components are `systemprompt.md` (required), `tool_guidance.md`,
`memory.md`, and `skills/NAME/SKILL.md`. Skills require exactly `name` and
`description` frontmatter fields. Limits are 24,000 always-on bytes, 128 KiB of
component text and 35 files. Scripts, attachments, plugins, hooks, subagents,
guardrails and provider configuration are rejected. Profile storage refuses
symlinks and escaping paths. Profile data is never evaluated as host code.

OpenCode2 receives advisory instructions before the unchanged original task.
Skills are advertised with absolute paths outside the checkout and loaded by a
file-read tool. Profiles cannot replace the agent's trusted system policy or
model configuration. The native Cube canary verifies the pinned binary actually
reads a mounted skill through its tool loop with a deterministic local provider.

Import an unapproved candidate with `factory profile import --file candidate.json`.
`factory profile inspect NAME_OR_DIGEST` prints its immutable content. Bind a name
under `[harness_profiles]`, for example `candidate = "sha256:..."`, and grant that
name explicitly with `scopes.SCOPE.harness_profiles = ["candidate"]`.

Ordinary `factory run --harness-profile NAME` requires signed promotion. Offline
experiments can evaluate operator-pinned candidates before promotion; that
exception cannot be requested through a normal run. The same name is available
as `harness_profile` in reviewed `factory apply` manifests.

## Offline experiments and comparison

Register an absolute suite JSON path under `[improvement_suites]`. Suites have
schema `esf-improvement-suite/v1` and a `fixtures` list. Each fixture contains
`id`, `group` (repository-lineage/task-family cluster), `split` (`development`,
`validation`, or `qualification`) and a standard `request` with original task,
exact committed revision, repository, OpenCode2 harness, scope and verification
profile. Use operator-owned acceptance gates; check baseline failures for repair
tasks. Do not place reference solutions or private grading materials in input
workspaces. Duplicate normalized tasks and clusters crossing splits are rejected.

Provide representative Go, Python and JavaScript cases for repairs, features,
testing and failure recovery. A real production corpus must be curated from
owned work with independently verified outcomes; synthetic unit fixtures are
not a substitute. Freeze its digest before qualification. At least 100 untouched
qualification tasks, three repeats, and ten independent clusters are required.

Run `factory improve --experiment-id ID --suite NAME --policy OFFLINE_POLICY
--partition development --wait`. Once a reusable candidate is pinned in the
offline policy, `--compare --partition qualification --repeats 3` runs the current
single baseline, static candidate, Harness2 refinement and four independent base
attempts. The repeated baseline selects the first verified nonempty patch.
Experiment IDs reject reuse; saved histories resume after worker restart, with
frozen corpus/configuration checks. Changing inputs requires a new experiment ID.

Temporal schedules a baseline, general and domain editor sessions, two parallel
probes, composition, composed execution and draft reflection. Each solver starts
from the same revision in a fresh VM. At most two VMs per task are concurrent;
editor VMs are destroyed before probes. Activities never retry model execution.
Only trajectories, patches and instructional components reach editors; gate
verdicts, scores and acceptance receipts do not. Truncation is marked explicitly.

Probes cannot be launched as top-level workflows, request review, approve a
change or update the Change index. A composed patch must pass existing gates and
cleanup. Otherwise a verified nonempty baseline can be selected. Unknown session
state, missing required evidence, budget faults and cleanup failures stop the
task and suspend the improvement circuit. The selected patch is copied into a
fresh VM, checked again, and enters the original review gate. Reflection creates
a draft artifact only; subsequent improvement sees a frozen profile/library
snapshot. Reusable lessons enter future profiles through reviewed promotion.

Static and adaptive qualifications and approval renewals append separate signed
receipts under `promotions/PROFILE_DIGEST/RECEIPT_DIGEST.json`. Activation checks
the current provider route as well as the model/runtime binding. A receipt from
another route or expired reviewer approval cannot activate a profile.

## Gateway and bounded policies

Use the overlay example in `deploy/harness-improvement/example.toml` with your
existing factory configuration. Keep provider credentials out of this file.
The editor and solver are pinned, preinstalled, CubeEgress-managed OpenCode
harnesses. Their normal provider URL can remain unchanged. Improvement sessions
use the declared gateway model resource and per-stage CubeEgress credentials.
All tools and dependencies must already be in the frozen template; improvement
admission rejects workspace package installation and setup scripts.

Configure separate 32-byte-or-longer gateway control and token secrets. Only the
gateway process receives the upstream provider key. `factory budget-gateway
--state /durable/ledger --upstream HTTPS_V1_URL --upstream-key-env SECRET_NAME`
binds loopback. Put a private TLS reverse proxy in front of the model endpoint;
keep `/control/` accessible only to trusted workers. CubeEgress allows only
`POST /v1/chat/completions`, pins Host/SNI, and injects an HMAC account token.
The model sees a placeholder. No gateway control credential enters a VM.

The initial protocol supports text Chat Completions only. It rejects media,
unqualified fields, model changes and oversized requests, enforces output caps,
requests streaming usage, and buffers responses until authoritative terminal
token usage is available. The input bound is conservative encoded request bytes
plus 4,096 framing tokens, with at most 128 messages. Qualify this bound against
the selected provider/tokenizer. Hidden provider tokens or unsupported protocols
must not be enabled under this route. Cache discounts are not assumed; recorded
microdollar costs round up at the approved full token rates.

`factory budget-gateway digest` computes the route identity from the upstream,
wire model, rates, pricing-evidence digest and protocol. The configured digest
must match. ESF's `provider/model` identity is kept distinct from the wire model.
The route and actual subscription billing/pricing must be independently checked
before using the resulting cost evidence for qualification.

Eight disjoint stage accounts reserve their complete request envelopes before
the round starts. Their limits sum to at most the parent limit, including
reflection. Each request reserves worst-case input/output cost durably before
forwarding; successful token evidence settles it. Concurrent workers use file
locks and atomic writes on shared durable POSIX storage. Crashes retain pending
reservations. Missing/out-of-bounds usage retains the reservation and revokes
the account. The gateway does not automatically retry upstream calls.

Policies have an explicit `enabled` flag. Live `shadow`/`adaptive` policies require
an absolute scope budget, a reviewed adaptive quality receipt, and cost/time
limits no greater than 5x the frozen matching baseline. Verification and cleanup
must fit. A round that cannot fit is rejected before VM allocation. Shadow keeps
the baseline result. Adaptive can select the verified composition. Default live
enrollment is 1%; 5% requires an independently signed pilot receipt covering at
least seven days and 100 tasks. Larger percentages are rejected.

## Qualification, promotion and rollback

Produce a `QualityInput` JSON from retained experiment evidence plus independent
human acceptance. It includes `split: qualification`, profile/runtime binding,
source commit, corpus/library/route digests, mode and per-trial task cluster,
repeat, arm, verification, acceptance, serious defects, known cost, duration and
evidence digests. Adaptive reports also bind the policy digest and include the
`repeated-baseline` arm. `factory profile qualify --input trials.json --output
quality.json` recomputes the paired cluster bootstrap with a deterministic seed.

Promotion requires at least five percentage points of accepted-task gain and a
positive lower 95% confidence bound. Adaptive must beat repeated baseline too.
Static mean cost and p95 duration must remain within 20%; adaptive within 5x.
Serious candidate defects, missing usage, insufficient data and acceptance after
failed verification all fail qualification. Reports preserve raw trials.

Configure independent Ed25519 public keys under `[improvement.approval_keys]`.
`factory profile approval-payload --candidate DIGEST --report quality.json
--reviewer KEY_ID --expires RFC3339` emits the exact bytes to sign without a
trailing newline. An independent reviewer signs those bytes with their own
Ed25519 key and sets `signature` to base64 in the resulting JSON receipt.
`factory profile promote --candidate DIGEST --report quality.json --approval
approval.json` recomputes quality and checks signature, author/reviewer separation,
binding and expiry. It retains a receipt; enabling a profile still requires an
operator configuration change. Never use unit-test signing keys as trusted keys.

For the 5% rollout, store a signed `RolloutReceipt` in the profile store's
`rollouts/DIGEST.json`, where DIGEST is the SHA-256 of its canonical JSON without
the `sha256:` prefix. It binds policy/scope, first/last pilot timestamps, task
count, evidence digest, reviewer and expiry. Signature covers the same object
with an empty signature field. The policy references the complete signed digest.

Monitor acceptance, verification failures, cost, p95 latency, fallbacks, unknown
usage, evidence failures and leaked VMs. Parent receipts and stage accounts keep
all usage distinct from the final materialization run. Critical failures create
`circuits/POLICY_DIGEST.json` and block subsequent adaptation. Reconcile stages,
gateway reservations and owned sandboxes before manually archiving a circuit
record. Do not reset spent/unknown ledger entries to retry work.

Rollback disables new admissions, restores the previous immutable profile name,
and uses existing factory cancellation/halt commands. Cancelled children receive
cancellation rather than termination. Cleanup revokes stage accounts and sweeps
owned VM metadata, including ambiguous creates. Preserve all evidence and rely
on finite provider TTLs plus reconciliation for infrastructure failures.

Production release qualification remains separate: exact source/assets, VM and
Kubernetes acceptance, replay, restore, security, load and 24-hour soak must pass.
An enabled `harness_improvement` companion-manifest section additionally binds
the upstream, profile, policy, route, quality report, independent approval,
component activation, budget enforcement, scope isolation and cleanup evidence.
Binary qualification alone does not enable this capability.

For local validation, compile test binaries on the host with `go test -c`, then
run them through `tools/cube-test` in the local Cube stack. The driver retains
binary/source hashes and verified cleanup. Project tests never execute on the
development host.
