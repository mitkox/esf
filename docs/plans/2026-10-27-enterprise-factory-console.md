# Enterprise factory console

Status: implemented and locally verified (2026-10-27); independent review pending.
Owner: factory console goal round.

Baseline: primary checkout at `50e23aa` with pre-existing unrelated changes
(component refresh, OpenCyber harness, compound-engineering files). This change
is additive: a new `internal/console` Go package, a new `internal/console/web`
React application, a `factory console` command, and narrow Makefile/docs wiring.
It does not modify the inherited Machinist control plane, and it survives the
`remove-machinist` direction: the console is a factory-first surface.

## Goal

Give the operator one enterprise-grade administrative surface for the whole
software factory: see every run, change, review, security stop, sandbox cleanup
and configured resource on one screen, then drill into the exact evidence behind
any number.

The console is a **read-only evidence and command surface** in this round. It
never invents data: every number is derived from the durable artifact store
(`storage.data_dir`: `runs/`, `changes/`) or from the loaded `factory.toml`.
Actions that mutate factory state (review decisions, cancel) stay in the
authenticated `factory` CLI until a separate, audited action path is designed.

## Personas and jobs

| Persona | Job to be done | Console answer |
| --- | --- | --- |
| Factory admin | "Is the factory healthy right now?" | Overview KPIs, live probes, attention queue, security posture |
| Reviewing engineer | "What needs my decision, and is the patch good?" | Review queue, run detail: gates, patch, evidence, previews |
| Platform operator | "Which worker/harness/sandbox/budget is drifting?" | Operations, catalog, sandbox cleanup, limits |
| Security reviewer | "Did any run step outside its boundary?" | Security page: findings, tripped runs, egress observations |
| Auditor | "Prove what happened for run X." | Run detail: immutable manifest, conditions timeline, artifacts, audit JSONL |

## Information architecture

```
Overview        Mission control: KPIs, throughput, outcomes, attention, health
Dispatch        Governed run-request composer; generates the exact CLI command
Runs            Searchable evidence ledger of every activation
  Run detail    Manifest, conditions, gates, security, artifacts, patch, audit
Changes         Durable work items with attempts and aggregate spend
  Change detail Activations, lineage, review state
Reviews         Human review queue (paused runs + changes awaiting decision)
Security        Cross-run hardening report, egress posture, alerts
Operations      Sandbox cleanup, infrastructure, harnesses, verification
Settings        Redacted operator configuration, storage, retention, about
```

`/#/overview` is the default route. Navigation is grouped **Factory** (Overview,
Dispatch, Runs, Changes, Reviews) and **Governance** (Security, Operations,
Settings).

## UX system

Enterprise means dense, legible, keyboard-first and predictable, not decorative.

- **Visual language**: deep graphite surfaces, 1px hairline borders, 14px radii,
  Manrope throughout, tabular numerals for metrics, mono for identifiers.
  Semantic tones: success (emerald), warning (amber), danger (rose), security
  (violet), info (sky). Dark and light themes share tokens; dark is default.
- **Layout**: fixed 248px sidebar, 56px top bar, content max 1600px, 12-column
  responsive grid. Below 1024px the sidebar becomes a drawer; below 768px cards
  stack and tables scroll horizontally.
- **Navigation**: global command palette (`Ctrl/Cmd-K`), `g o/r/c/v/s/p` style
  shortcuts, breadcrumbs, browser back/forward via hash routing, deep links for
  every run/change.
- **State**: skeleton loading, inline retry on error, explicit empty states,
  "last updated" timestamp, auto-refresh (10–30s pages, 5s in-flight run
  detail), stale-data banner when the index refresh fails.
- **Tables**: sticky header, row hover, sortable columns where the server
  supports it, filters as chips, page-size control, visible result count.
- **Accessibility**: skip link, focus-visible rings, aria labels on icon
  buttons, `prefers-reduced-motion`, no color-only status encoding (dot + text),
  keyboard operable palette/tabs/tables.
- **Trust**: every card labels its evidence source and coverage window; no
  estimated cost or invented success rate. Missing harness token reports show
  "not reported", never zero.

## Data sources (authoritative)

| Source | Used for |
| --- | --- |
| `runs/<id>/run.json` (`factory.RunManifest`) | every run KPI, outcome, duration, tokens/cost when reported, hardening evidence, cleanup result, conditions |
| `runs/<id>/task.json` | task specification on run detail (bounded, redacted evidence) |
| `runs/<id>/verification/result.json` | deterministic gate steps on run detail |
| `runs/<id>/changes.patch` + `hardening/patch-scan.json` | patch size/stat preview, patch security scan |
| `runs/<id>/audit/actions.jsonl` | privileged operator actions (attach, preview, review decision) |
| `changes/*.json` (`factory.Change`) | change lifecycle, attempts, lineage, aggregate spend |
| `factory.toml` (`factory.LoadConfig`) | catalog, limits, harnesses, verification profiles, hardening/review posture |
| `factory.BuildThreatReport` | cross-run security aggregate |
| TCP reachability probes | Temporal / Cube / OTLP endpoint status (labelled as probes, not health claims) |

The index is built by scanning the artifact directories, caching manifests by
mtime+size, and refreshing every 15 seconds (and on demand after a failed read).
A refresh is incremental: unchanged files are never re-parsed. The current demo
store has ~463 runs and ~778 changes; the design target is 10k runs with
sub-second list queries because filtering/sorting happens on an in-memory index
of small summaries, not by re-reading files per request.

## Authentication and network posture

- The console binds to `127.0.0.1` by default. A bearer read token
  (`--read-token-file`, private file, 32+ chars) is required for any non-loopback
  bind. `--no-auth` is refused unless the listener is loopback, and is intended
  for local evaluation only.
- Token comparison is constant-time. Tokens are never logged, never placed in
  URLs, and the SPA keeps them in `sessionStorage` (tab-scoped), attaching them
  as `Authorization: Bearer`.
- API responses are `Cache-Control: no-store`; the SPA index is no-store and
  hashed assets are immutable. Strict CSP, `X-Frame-Options: DENY`,
  `Referrer-Policy: no-referrer`, `Permissions-Policy` deny-all.
- Read endpoints only: no writes, no shell execution, no Temporal connection in
  this round. Artifact reads are allowlisted to known evidence paths, confined to
  the run directory, size-capped, and text-only.
- Catalog responses are explicit DTOs, so credentials (`cube.api_key`, model
  keys, harness env values) cannot leak by accident.

## API contract (`/api/v1/console`)

All JSON unless noted. Errors: `{"error":"...","code":"..."}` with 4xx/5xx.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/healthz` | unauthenticated liveness: `{"status":"ok","version":...}` |
| GET | `/session` | auth probe: environment, versions, server time, capabilities |
| GET | `/overview?window=24h|7d|30d|90d|all` | KPIs, series, outcomes, harness/repo/scope breakdowns, attention, recent runs, security + health summary |
| GET | `/runs` | paginated/filtered run summaries: `query,state,harness,repository,scope,window,sort,limit,cursor` |
| GET | `/runs/{id}` | run summary + manifest + task + verification + patch summary + artifact list + audit + related change |
| GET | `/runs/{id}/patch` | `text/plain` unified diff (2 MiB cap) |
| GET | `/runs/{id}/artifacts?path=` | allowlisted text evidence (512 KiB cap) |
| GET | `/changes` | paginated/filtered change summaries: `query,status,repository,scope,window,sort,limit,cursor` |
| GET | `/changes/{id}` | change + activation runs + review context |
| GET | `/reviews?limit=&cursor=` | paginated paused runs and in-review changes with waiting time and CLI commands |
| GET | `/security?window=` | threat report, egress observations, hardening posture, alerts |
| GET | `/operations` | sandbox cleanup summary, infrastructure, harnesses, verification profiles, limits, probes |
| GET | `/catalog` | redacted operator configuration |
| GET | `/search?q=&limit=` | runs + changes for the command palette |

Summary shapes shared by list and overview:

```json
{
  "run_id": "run-…", "change_id": "…", "parent_run_id": "",
  "scope": "default", "repository": "github.com/acme/api",
  "repository_kind": "remote", "requested_revision": "…", "baseline_sha": "…",
  "agent_harness": "opencode2", "verification_profile": "default",
  "factory_result": "SUCCEEDED", "agent_result": "SUCCESS",
  "verification_result": "SUCCESS",
  "cleanup_result": {"outcome": "SUCCESS", "verified": true, "remaining_sandboxes": 0},
  "started_at": "RFC3339", "completed_at": "RFC3339", "duration_ms": 0,
  "review_configured": false, "review_outcome": "",
  "budget_exceeded": [], "tokens_in": null, "tokens_out": null,
  "inference_cost": null, "sandbox_id": "…",
  "hardening": {"tripped": false, "findings": 0, "max_severity": "",
                 "egress_violations": 0, "gate_allowed": true}
}
```

```json
{
  "change_id": "…", "scope": "default", "repository": "…",
  "requested_revision": "…", "task_preview": "…", "task_hash": "…",
  "status": "IN_REVIEW", "attempts": 1, "total_cost_usd": 0,
  "total_tokens": 0, "last_run_id": "…", "last_result": "SUCCEEDED",
  "created_at": "…", "updated_at": "…", "rework_note": "",
  "activations": [{"run_id": "…", "reason": "initial", "parent_run_id": "", "created_at": "…"}]
}
```

## Acceptance criteria

1. `factory console` starts against the live artifact store and serves the SPA;
   `factory console --help` documents token, listener and data-dir flags.
2. Every route above returns correct, bounded JSON with token auth; unauthenticated
   requests get 401 with `WWW-Authenticate`; traversal attempts get 400/404.
3. Overview totals match an independent script over the same artifact store.
4. All ten screens render real evidence with no console errors at 1440×900 and
   390×844, with dark and light themes.
5. Keyboard: command palette opens with Cmd/Ctrl-K, searches runs/changes, and
   navigates; focus ring visible on all interactive elements.
6. `go build ./...` succeeds; console Go unit tests cover auth, path safety,
   filtering, aggregation and redaction; `npm run build` produces the embedded
   dist; the Vite bundle has no external network runtime dependencies (charting
   and the Manrope font package are bundled, not fetched).
7. Documentation: operator guide for the console (startup, auth, deployment
   behind a reverse proxy, data refresh, limits) in `docs/console.md` and a README
   pointer.
8. An independent reviewer verifies the candidate file list, security posture and
   acceptance evidence.

## Verification plan

- Host build is allowed: `go build ./...`, `go vet ./internal/console/...`,
  `npm run build`.
- Application execution for validation: run `factory console` on loopback as the
  intended host-side operator tool and exercise every route with Playwright
  (screenshots + console/network assertions). This is console execution, not
  project test execution; Go/JS unit suites remain CI/Cube responsibilities and
  will be reported as such if not executed in a sandbox this round.
- Cross-check headline KPIs with a throwaway Python aggregation over `run.json`
  (read-only) to prove the API does not drift from evidence.
- No Cube runtime change is required by this addition; no sandbox is created or
  mutated by the console.

## Rollout

Round 1 (this plan): read-only console with the Overview, Dispatch, Runs, Run
detail, Changes, Change detail, Reviews, Security, Operations and Settings
screens, bearer auth, live Temporal/Cube read-only integration, docs and tests.
Round 2 candidates: OIDC/SSO plus role mapping, an audited action bridge for
review approve/reject and run cancellation, saved views/annotations, CSV/JSON
export, and configurable alert webhooks from the console.

## Implementation record (round 1)

Baseline: `50e23aa` with unrelated dirty files. Candidate change set:

- Go: `internal/console/{server,handlers,aggregate,catalog,security,index,live,
  static,summary,types}.go` and `console_test.go`; `cmd/factory/console.go` and
  `console_test.go`; one-line command registration in `cmd/factory/main.go`;
  additive `factory.AggregateThreatReport` refactor in `internal/factory/threats.go`
  so the console aggregates cached manifests instead of re-reading the store.
- Web: `internal/console/web`, including checked-in `dist/**` build output
  (React 19 + Vite 8 + Tailwind v4; hand-written SVG charts; Manrope font
  bundled via `@fontsource-variable/manrope`, so there is no external network
  dependency at runtime; embedded via `go:embed`). Screens:
  overview, dispatch (generates the governed `factory run` command; never
  submits), runs, run detail, changes, change detail, reviews, security,
  operations, settings. Shared shell: command palette, attention queue,
  window/auto-refresh controls, theme toggle, keyboard navigation.
- Wiring/docs: `Makefile` console targets, `.gitignore` console entries,
  `README.md`/`docs/README.md` links, `docs/console.md`, CI console build/test
  steps.

Design decisions that matter:

- The console is read-only by construction: every API route is a GET, no route
  opens a sandbox, and secrets are removed by explicit catalog DTOs rather than
  by redacting a config dump.
- The evidence index is incremental (mtime+size reuse) and request handling
  never re-reads manifest files; list/overview/security queries operate on the
  in-memory projection. Live Temporal/Cube views are optional, bounded and
  cached, and degrade to an explicit "unavailable" state.
- Review decisions and cancellation remain in the authenticated CLI. The UI
  surfaces exact copyable commands, which is an auditable boundary instead of a
  second privileged control plane.
- Responses are gzip-compressed when requested; the SPA bundle drops from
  ~300 KB to ~92 KB and the overview payload from ~24 KB to ~4.7 KB over the
  wire.
- Production access is TLS-first: native HTTPS (`--tls-cert-file`/
  `--tls-key-file`, TLS 1.2 floor, HTTP/2, HSTS, world-readable-key refusal) or
  a terminating reverse proxy documented for nginx and Tailscale. Failed bearer
  attempts are throttled per client (10 in 5 minutes, one-minute lockout;
  X-Forwarded-For trusted only from a loopback proxy, with a bounded client
  table and overflow bucket) so a publicly reachable console cannot be
  token-guessed indefinitely or have its throttle state grown without limit.

Local verification evidence (2026-10-27):

- `go build ./...`, `go vet ./internal/console/ ./cmd/factory/`,
  `go test -c -o /dev/null ./internal/console ./cmd/factory`, `gofmt -l` on the
  candidate Go files, and `npm run build` (Vite) all pass.
- KPI cross-check: independent Python aggregation over `.factory/runs/*/run.json`
  for the all-time window matched the API exactly on runs (459), success rate
  (0.755991), security stops (2), cleanup verified rate (0.989107), tokens
  (64,582 / 6 samples), p50 (18,196 ms) and p95 (182,127 ms).
- Live console smoke: `factory console` served the loopback API with 401 for
  unauthenticated calls, `WWW-Authenticate`, `no-store` on API, CSP, gzip, and
  read-only evidence for every route. Headless Chromium checked 10 screens:
  67 structural/keyboard/mobile assertions passed with 0 console errors
  (invalid token rejected; Ctrl+K palette searches and Enter-navigates; `g r`
  navigation; `?` cheatsheet; theme toggle; attention and operator menus; no
  horizontal overflow at 1440×900 or 390×844).
- Layout/legibility audit: 0 contrast failures, 0 unintended clipped text,
  0 page overflow, 0 small tap targets after fixes (artifact path rows and the
  undelivered-alert link were enlarged).
- Screenshots for the operator: `.rtk/screens/*.png` (gitignored local evidence).
- Reproducible frontend install: `npm ci` against the checked-in lockfile
  rebuilds the same 46-file dist byte-for-byte; the Go binary embeds it.
- Degraded modes were exercised against dedicated loopback instances:
  `--live=false` renders the orchestration check as DISABLED (never OK) and
  withholds live sandbox totals; a deliberately broken evidence directory after
  a successful first load renders the stale-evidence banner, keeps the last
  complete projection, and marks the health index row as warning.

Validation limits retained honestly:

- Project unit suites (`go test`, `npm test`) were **not executed on the host**
  per the repository execution policy; they are compiled and wired into CI
  (`make test-console`, updated `.github/workflows/ci.yml`). CI/Cube execution
  remains the authoritative gate.
- No Cube runtime validation was performed: this change adds a read-only host
  tool and changes no factory execution path. No sandbox was created or mutated.
- Visual QA is structural/programmatic (Playwright + computed-CSS audit), not a
  human pixel review; the screenshot set is attached for human inspection.
- Remaining handoff gate: the change is intentionally uncommitted (the session
  has no commit authorization). `internal/console/web/dist/**` (46 files) is
  unignored and must be staged with the rest of `internal/console` when the
  change is committed, or the CI "tracked dist is current" check will fail.
  Independent review (`console-reviewer`, task-6) confirmed there is no code
  blocker/high finding left; this staging step is the only remaining gate.
- Independent review (`console-reviewer`, task-6) returned "not ready" on four
  high-severity issues plus mediums/lows. All were addressed before handoff:
  the two stale test assertions (duration sort, dispatch route coverage), the
  embedded-dist/clean-checkout packaging claim (dist is checked in and CI
  verifies it), health/leak overclaims when live data is unavailable (unknown and
  disabled checks are never described as passing; leak classification is
  withheld without workflow attribution), unreachable stale-evidence warnings
  (now `index.stale` + banner), reviews empty-state copy under unavailable live
  state, the non-windowed review queue caveat, zero-value timestamps, query/facet
  bounds, `null` security arrays, invalid `--refresh` handling, binary-diff
  detection, and the remaining nits. Final QA after fixes: 67 Playwright
  assertions pass with 0 console errors; layout audit reports 0 contrast,
  clipping, overflow and small-tap-target findings. Degraded-mode browser checks
  also confirmed that `--live=false` renders the orchestration check as
  DISABLED (never OK) and that a deliberately broken evidence directory produces
  the stale-evidence banner with the scan error while retaining the last complete
  projection.
