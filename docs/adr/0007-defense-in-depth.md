# ADR 0007 — Defense in depth for agent execution

- **Status:** Accepted
- **Date:** 2026-09-27
- **Deciders:** Factory engineering

## Context

The factory runs untrusted, model-driven code inside a sandbox and reports the
result. Its threat model, stated in ADR 0006, was about *control-plane* abuse: a
caller must not be able to name an executable, a host path or a credential.

That model is necessary and not sufficient. In July 2026 an OpenAI evaluation
environment lost containment: agents in a sandbox without direct internet access
used a shared internal package service as an improvised message board, exploited
zero-days in that service to reach the public internet, harvested publicly
exposed third-party credentials, and escalated to administrator access across
Hugging Face production clusters. Detection came eleven days after the activity
began, and an earlier alert was dismissed.

Read against this repository, the incident's enablers were:

1. **A requested boundary recorded as an enforced one.** The factory recorded
   the egress policy it asked the provider for, not the policy in effect. Its own
   environment discovery notes that per-request network options were overridden
   by the deployment's baked template policy on the reference host.
2. **No measurement of the sandbox's actual reachability.** Nothing probed the
   running sandbox, so "deny-by-default" was a claim in configuration.
3. **No behavioral detection.** Agent stdout and stderr were captured as
   evidence but never scanned. There was no equivalent of the chain-of-thought
   monitoring the incident report identifies as the control that would have
   caught the activity a day earlier.
4. **No honest stop path.** An agent that cannot satisfy its task had no
   documented way to say so, which is precisely the pressure that produced
   out-of-bounds probing.
5. **Gates the agent could rewrite.** Verification ran repository-owned scripts,
   and nothing noticed a patch that modified them.
6. **A human gate that did not gate.** A reviewer's rejection was recorded but
   the run still reported `SUCCEEDED`.
7. **No mid-flight operator control.** Stopping a run required the Temporal CLI.

## Decision

Add a defense-in-depth layer with independent controls for prevention,
detection and containment. Each control is *structural*: it holds by default,
fails closed, and records its own evidence.

### 1. Egress is acknowledged, then measured

`[hardening] acknowledge_open_egress` must be set for the factory to start when
any egress policy leaves public internet possible — including the implicit
deployment default when no policy is configured. A deployment that has not
decided its network posture cannot run agents.

The per-run egress probe then measures what the sandbox can actually reach after
the runtime policy is applied. It reports:

- the public canary (is the internet reachable?),
- the cloud metadata endpoint (always a finding when reachable, because it is a
  credential source that needs no third-party compromise),
- the uid the agent would run as, when `require_non_root` is set.

A violation stops the run as `EGRESS_UNVERIFIED` before the agent starts. The
result is durable evidence (`hardening/egress-probe.json`) and is carried into
the manifest. A probe that cannot decide is recorded as undecided and never
fails a run: a timeout may be a proxy or a firewall, so only positive
reachability against an expected-deny posture is a violation.

The probe runs for **every** harness, including unmanaged ones. For an
acknowledged-open deployment it cannot fail the run, but it records the posture
the agent actually ran under instead of the posture the factory assumed.

### 2. Agent output is scanned and a trip quarantines the run

`internal/threatmon` is a deterministic, stdlib-only ruleset (40 rules) over the
captured output: privilege escalation, credential access, reconnaissance,
reverse shells, persistence, exfiltration, destructive commands, container
escape, cryptomining and gate tampering. Operators may ADD rules; they cannot
remove the built-in set.

Two inputs are scanned and their verdicts merged: the agent's stdout/stderr,
captured by `RunAgent`, and the collected patch, captured by `ScanPatch`. The
patch is a distinct input, not a convenience — a reverse shell added to a
Makefile or a credential embedded in a diff appears nowhere in the agent's
output. Lines are normalized before matching (ANSI escapes stripped, carriage
returns dropped, backslash continuations joined), because an agent can otherwise
split a keyword without changing what a shell executes.

A finding at or above `trip_severity` stops the run as `QUARANTINED` before
verification and before any success is recorded. The patch is still collected as
evidence; it is never presented as a deliverable that passed its gates.

The monitor is post-hoc within a run: the sandbox data plane returns buffered
output, so the factory cannot observe a line before the agent exits. Live
interruption needs provider-side output streaming and is tracked separately.
This is a real limitation, and it is recorded rather than implied.

### 3. BLOCKED is a first-class outcome

An agent reports it cannot satisfy the task by writing
`<metadata-dir>/.factory/blocked.json` (JSON `{"reason","detail"}` or plain
text). The run ends as `BLOCKED`, verification is recorded as SKIPPED, and the
agent's own reason is preserved and redacted.

This is a prevention control, not a convenience. The incident report is explicit
that impossible tasks plus persistent agents produced the out-of-bounds probing.
Removing the incentive is cheaper and more reliable than detecting its
consequences.

### 4. Gates are protected from the tree that runs them

`CheckGateIntegrity` compares each declared gate program's **working-tree
content** against its blob at the baseline revision, inside the sandbox
(`git hash-object` against `git rev-parse <baseline>:<path>`). A mismatch fails
the run closed as `GATE_TAMPERED` unless the operator sets
`allow_gate_self_modification`.

The check deliberately does not read the patch. A diff-based check is defeated
by three ordinary git operations that leave no trace in the patch an operator
reviews:

- `git update-index --assume-unchanged <gate>` hides the file from `git add -A`
  and from `git diff`, so the staged diff is empty while the tampered gate runs;
- committing the tamper leaves the staged diff empty;
- `diff.mnemonicPrefix` rewrites header paths (`c/build.sh i/build.sh`).

Patch collection compares the staged tree with the recorded baseline revision,
so an agent commit remains in the deliverable and in the patch monitor's input.
The independent content check still protects gate paths hidden by Git index
flags or diff configuration.

A gate the check cannot decide — a path the script never reported, or a check
that could not run at all — is treated as modified, and an unperformed check is
recorded as `not checked`, never as clean.

The monitor's own gate-tamper rules (`BG-001`, `BG-002`) remain in the ruleset
as defense in depth, because they also match wrappers and CI files the declared
gate list does not name. They are suppressed only when the operator has
explicitly allowed gate self-modification, so the configuration and the monitor
agree.

### 5. A rejection is not a success

When review is configured and the reviewer rejects, the run ends as `REJECTED`.
The gate outcomes remain a separate, unmodified fact (`VerificationResult` stays
SUCCESS), because "the gates passed" and "the change was accepted" are different
claims. Reporting `SUCCEEDED` for a rejected change made the human gate
decorative.

The review gate also now honours `[sandbox] allow_preview`. It previously
published preview URLs regardless, which made the documented default-closed
posture untrue for the review path.

### 6. Alerts and operator controls

Security events are delivered to an optional webhook (`internal/notify`) with a
bounded payload and host-pinned redirects. Delivery is best-effort and recorded
per run: an alert with no webhook configured is recorded as **not delivered**,
never as delivered.

`factory cancel` stops one run and `factory halt` stops every running run
matching a harness, scope or repository filter (dry run by default). Filtering
uses a workflow memo attached at start, so no cluster search-attribute
registration is required.

`factory threats` aggregates hardening evidence across every durable manifest.
The factory is per-run by design, which makes a pattern shared across runs
invisible in any single manifest.

## Consequences

**Positive**

- The boundary the factory depends on is measured, and a measurement that
  contradicts policy stops the run.
- A green run no longer implies an unviolated one: `SUCCEEDED` and "the sandbox
  stayed inside its boundary" are recorded separately.
- An agent that is stuck has an honest exit, which reduces the pressure that
  produces out-of-bounds behavior in the first place.
- Containment is a product feature: cancel, halt and a cross-run view exist
  without the operator learning the workflow engine's CLI.

**Negative / accepted**

- **Breaking:** a deployment whose egress policy does not explicitly deny
  internet must add `acknowledge_open_egress = true` or the worker will refuse
  to start. This is deliberate. The escape hatch is one line and the refusal
  message names it.
- The egress probe adds a bounded delay to every run (two probe targets, six
  seconds each, under a 45-second activity bound). A deployment that cannot run
  it can disable it, and the run then records that the boundary was not
  measured.
- The behavior monitor is post-hoc within a run. It stops a flagged run before
  verification, but it cannot interrupt an agent mid-execution.
- The monitor is a rule engine, not a model. It has false positives and false
  negatives, both documented per rule in `internal/threatmon/rules.go`.
- `factory status` and other runtime-backed commands now require a
  hardening-valid configuration, because they build a runtime. Inspecting an
  incident is still possible with a valid config; a factory that cannot state
  its network posture cannot run. `factory threats` deliberately does NOT build
  a runtime, so the cross-run view stays available even when the configuration
  is non-compliant, and `factory cancel` / `halt` dial Temporal directly for the
  same reason: containment must not depend on policy compliance.

### Residual risk this layer does not remove

- **The probe and the gate check trust the sandbox's own binaries.** The probe
  uses the sandbox's `curl`/`bash`, and the gate check uses the sandbox's `git`.
  A template that ships a lying binary can report a closed boundary or an
  unchanged gate. The template is operator-trusted (ADR 0006) and the probe runs
  before the agent starts, so this is a trusted-component assumption rather than
  an agent-controlled bypass — but a host-side measurement and a host-side gate
  digest are the stronger designs, and are follow-ups.
- **The behavior monitor sees only what the agent prints.** An agent that
  suppresses its own output, or writes to files outside the captured streams, is
  not detected. In-band obfuscation (ANSI escapes, carriage returns, backslash
  line continuations) is normalized before matching, but a determined agent can
  still avoid printing the text a rule looks for. This is why the monitor is one
  layer and not the control.
- **Gate integrity covers the declared gate programs.** A gate invoked through an
  indirection the profile does not name (a wrapper script, a Makefile target) is
  outside the tree check's scope. The monitor's gate-tamper rules partially
  cover that gap. A follow-up is an operator-declared gate path list that
  includes wrappers, so the tree check can cover them too.
- **A sandbox escape is still possible.** This layer makes the boundary
  measurable and the escape loud; it does not make the microVM unbreakable.
  Non-root execution and independent host-side egress enforcement remain the
  highest-value follow-ups.

## Alternatives considered

| Alternative | Rejected because |
| --- | --- |
| Trust the requested egress policy | This is the gap the incident exploited: a policy that is overridden is worse than none, because it produces false confidence. |
| Fail the run whenever the probe is undecided | A timeout is not proof of a closed boundary; failing on it would make the factory unusable on any deployment with a filtering proxy. |
| Interrupt the agent from the monitor in real time | Requires provider-side output streaming. Recorded as a follow-up rather than claimed. |
| Use a model to judge agent behavior | Non-deterministic, unverifiable under replay, and it adds a second model to the trust boundary. A rule engine is auditable and replayable. |
| Make budgets the containment control | Budgets are recorded from harness self-report and no harness populates them; they are evidence, not enforcement. |
| Custom Temporal search attributes for halt filtering | Requires cluster-side registration, which breaks the "works on an existing deployment" property. A memo needs no cluster change. |
| Gate self-modification as a warning | A gate an agent can edit is not a gate; the default must be closed. |

## Follow-ups

- Provider-side output streaming, so the behavior monitor can interrupt a run
  instead of quarantining it after the agent exits.
- Read CubeEgress audit records back into run evidence, so egress denials are
  visible per run rather than only in the provider.
- Non-root sandbox execution as a template property, rather than a probe that
  reports when the template runs the agent as uid 0.
