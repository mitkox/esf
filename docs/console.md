# Factory admin console

The factory console is the operator-facing administrative surface for ESF. It
serves a single-page React application from the `factory` binary and exposes a
read-only JSON API over the durable artifact store.

It is deliberately **not** a second control plane:

- No endpoint mutates a run, change, sandbox, review decision or configuration.
- Review and cancellation actions remain in the authenticated `factory` CLI.
  The console shows the exact command to copy for each action.
- It never opens a sandbox and never talks to the Cube data plane except for a
  bounded, read-only sandbox inventory (which can be disabled with `--live=false`).

## Start the console

Generate a read token and keep it in a private file:

```sh
mkdir -p ~/.config/esf
openssl rand -hex 32 > ~/.config/esf/console.token
chmod 600 ~/.config/esf/console.token
```

Serve the console (defaults: loopback `127.0.0.1:7331`, 15 second evidence
refresh, live Temporal/Cube queries enabled):

```sh
./bin/factory console \
  --read-token-file ~/.config/esf/console.token \
  --environment production
```

Open `http://127.0.0.1:7331` and enter the token. The token stays in the browser
tab's `sessionStorage` and is sent as an `Authorization: Bearer` header.

Flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--listen` | `127.0.0.1:7331` | Listen address. A non-loopback address requires a read token. |
| `--read-token-file` | empty | Private, 0600 regular file containing a 32+ character token. |
| `--tls-cert-file` | empty | PEM certificate that enables native HTTPS (requires `--tls-key-file`). |
| `--tls-key-file` | empty | PEM private key, mode 0600 (no group/other access), paired with the certificate. |
| `--no-auth` | false | Disable auth. Only allowed on a loopback address; local evaluation only. |
| `--live` | true | Enable bounded Temporal workflow queries and Cube sandbox inventory. |
| `--environment` | `$FACTORY_ENVIRONMENT` or `self-hosted` | Badge shown in the UI. |
| `--data-dir` | `storage.data_dir` from config | Artifact store root to read. |
| `--refresh` | `15s` | Maximum age of the in-memory evidence index. |
| `--config` | `./factory.toml` | Factory configuration (global flag). |

Native HTTPS (the production single-host shape):

```sh
./bin/factory console \
  --listen 127.0.0.1:8443 \
  --read-token-file ~/.config/esf/console.token \
  --tls-cert-file ~/.config/esf/tls/console.crt \
  --tls-key-file ~/.config/esf/tls/console.key \
  --environment production
```

The certificate must be valid for the hostname clients use. The private key is
refused if it is world-readable. TLS 1.2 is the floor, HTTP/2 is negotiated, and
every response carries `Strict-Transport-Security: max-age=31536000` when the
console terminates TLS itself. Certificate and key can also be supplied through
`FACTORY_CONSOLE_TLS_CERT_FILE` and `FACTORY_CONSOLE_TLS_KEY_FILE` so a service
manager does not have to put paths in the process argument list.

Local evaluation without a token (loopback only, never for a shared host):

```sh
./bin/factory console --no-auth --environment development
```

## Build

The SPA is embedded in the binary. A release build is:

```sh
make console-build   # npm ci + vite build + go build
```

On a machine without Node.js, `make build` works when
`internal/console/web/dist` is checked in with the source (as the inherited
console assets were), so the Go binary builds on a machine without Node.js. CI
rebuilds the SPA from source and verifies the checked-in output is current.

## Screens and evidence sources

| Screen | Question it answers | Evidence |
| --- | --- | --- |
| Overview | Is the factory healthy, fast and safe right now? | All manifests in the window, `changes/`, live Temporal/Cube status, configuration posture |
| Dispatch | What exact run request would I submit, and what happens next? | Loaded operator catalog and recent run evidence; generates a CLI command, never submits |
| Runs | What happened, and why? | `runs/<id>/run.json`, conditions, verification result, patch, hardening, audit |
| Changes | What work is in flight across attempts? | `changes/<id>.json` plus each activation's manifest |
| Reviews | What needs a human decision? | Changes in `IN_REVIEW`, paused live workflows, per-run preview links |
| Security | Did any run step outside its boundary? | Per-run hardening evidence aggregated across the window |
| Operations | Are sandboxes, budgets, harnesses and infrastructure in order? | Cleanup evidence, live Cube inventory, configuration, TCP probes |
| Settings | What policy is this factory running? | Redacted `factory.toml` catalog |

Missing evidence is always shown as **not measured / not reported**, never as a
zero or a pass. For example, a harness that reports no token usage contributes
nothing to the token KPI, and the card says so.

## Data refresh and scale

The console builds an in-memory index of run manifests and change records at
startup, then refreshes incrementally: each request triggers at most one refresh
when the index is older than `--refresh`, and unchanged files are reused by
mtime and size. List, overview and security queries read only the index.

Practical bounds:

- Run and change lists are paginated (25 by default, 100 maximum).
- Reviews are paginated the same way.
- Patch download is capped at 2 MiB; artifact previews are capped at 512 KiB
  (long logs return their tail).
- The security view returns at most 100 entries, observations and stops; the
  aggregate counts always cover every inspected manifest.
- The index is proportional to the number of evidence records (a few kilobytes
  per run). 10k runs fit comfortably in process memory; the design cost is the
  refresh scan, not request handling.

## Live integrations

When `--live=true`:

- Temporal is queried for running workflows. A workflow that has no durable
  manifest yet can still be opened from the run URL; its detail page labels the
  state as live and shows the workflow's own conditions query.
- CubeSandbox is queried for the live sandbox inventory. A factory-owned sandbox
  is flagged as a **leak** when its run has durable evidence but no open
  workflow, which is exactly the condition that should never happen.

Both calls are bounded (6 second ceiling), cached (5–20 seconds) and fail
independently. An unreachable backend degrades the relevant card to
"unavailable" with the reason; it never blocks the rest of the console. Set
`--live=false` for a strictly offline console.

The overview also runs bounded TCP reachability probes against Temporal, the
Cube API and an OTLP endpoint if configured. They are labelled **probes**, not
health claims: a reachable port is not the same as a healthy service.

## Security posture

- Loopback by default. A read token is mandatory for any other listener; the
  server refuses to start without one.
- Constant-time token comparison; the token is never logged or placed in a URL.
- Failed bearer attempts are throttled per client: 10 failures in 5 minutes
  block the client for one minute (`429` with `Retry-After`). The first
  `X-Forwarded-For` address is used only when the TCP peer is loopback (a local
  nginx/Tailscale proxy); direct and non-loopback clients are keyed by their
  peer address, so forging the header cannot reset the counter. Throttle state
  is bounded (4096 tracked clients plus a shared overflow bucket), so
  unauthenticated traffic cannot grow it without limit.
- Native TLS requires a certificate/key pair, refuses private keys readable by
  group or other (mode 0600), enforces a TLS 1.2 floor, and adds HSTS when it
  terminates TLS.
- Strict Content Security Policy, `X-Frame-Options: DENY`, `no-referrer`,
  denied browser permissions, no CORS.
- API responses are `Cache-Control: no-store`; hashed static assets are
  immutable.
- Artifact reads are allowlisted to known evidence paths, confined with
  `os.OpenRoot`, size-capped and text-only.
- Catalog responses are explicit DTOs, so `cube.api_key`, model credentials and
  harness environment **values** cannot be serialised even if the underlying
  config gains new secret fields. Only credential names and presence booleans
  are exposed.
- Request bodies are ignored; every endpoint is a GET.

### Behind a reverse proxy

Terminate TLS in front of the console and keep the console itself on loopback.
This is the preferred shape when an ingress/load balancer already owns
certificates and access control:

```nginx
server {
  listen 443 ssl;
  server_name factory.example.com;

  location / {
    proxy_pass http://127.0.0.1:7331;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    # Overwrite, do not append: the console trusts X-Forwarded-For from its
    # loopback proxy, so a client-supplied value must never survive.
    proxy_set_header X-Forwarded-For $remote_addr;
  }
}
```

A shared deployment should additionally put SSO in front of the proxy and keep
the bearer token as a second factor until role-aware authentication lands (see
the roadmap in `docs/plans/2026-10-27-enterprise-factory-console.md`).

### Tailscale HTTPS / funnel

A tailnet serves the console over HTTPS without another certificate manager.
Point Tailscale at the console port (plain loopback is fine because the link is
local, and Tailscale terminates the publicly trusted certificate):

```sh
# Tailnet-only (recommended): https://<host>.<tailnet>.ts.net/
tailscale serve --bg --https=443 http://127.0.0.1:7331

# Public funnel, if the operator explicitly wants internet reachability.
# Keep the bearer token and the auth throttle; Tailscale supplies
# X-Forwarded-For and the console trusts it because the peer is loopback.
tailscale funnel --bg --https=8443 http://127.0.0.1:7331
```

If the console terminates TLS itself and Tailscale should proxy to it, use the
`https+insecure` target form because the Tailscale edge validates its own
certificate, not the loopback one:

```sh
tailscale funnel --bg --https=8443 https+insecure://127.0.0.1:8443
```

### systemd

Run the console as its own service so it restarts with the host and keeps the
token out of the unit file:

```ini
# ~/.config/systemd/user/esf-console.service
[Unit]
Description=ESF factory console
After=network-online.target

[Service]
Type=simple
WorkingDirectory=%h/dev/esf
ExecStart=%h/dev/esf/bin/factory console \
  --config %h/dev/esf/factory.toml \
  --listen 127.0.0.1:8443 \
  --read-token-file %h/.config/esf/console.token \
  --tls-cert-file %h/.config/esf/tls/console.crt \
  --tls-key-file %h/.config/esf/tls/console.key \
  --environment production
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%h/dev/esf/.factory

[Install]
WantedBy=default.target
```

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| `401` after login | Token changed on the server | Re-enter the token; it is not cached across restarts |
| Empty overview with a health warning | Wrong `--data-dir`, or no evidence on this host | Check the "Evidence store" health card and the path printed at startup |
| "Live orchestration unavailable" | Temporal unreachable or TLS/mTLS misconfigured | Fix Temporal connectivity, or run with `--live=false` |
| "Not reported" cost/tokens | The harness did not report usage | Expected for conformance and some harnesses; never estimated |
| Run opens with a "live orchestration state" banner | The workflow is still executing | The durable manifest appears when the run finishes |
| Review list is very large | The deployment accumulated `IN_REVIEW` changes; the queue is intentionally not windowed | Page through the queue and clear decisions in the CLI |
| Console refuses to start with a TLS error | Certificate expired/not-yet-valid, mismatched key, or world-readable key | Check `openssl x509 -enddate`, key permissions (`chmod 600`), and that both flags are supplied |
| `429 Too Many Requests` | More than 10 failed bearer attempts from one client in 5 minutes | Wait one minute, or fix the token; the block is per client and temporary |

## Validation limits

The console is host-side operator tooling. Its Go package builds and is vetted
on the host; the SPA is built with Vite. API behaviour is exercised against the
local artifact store and browser-checked with Playwright. Running the console is
not a factory execution: it creates no sandbox and changes no run evidence.

## Production checklist

1. Generate a 32+ character token in a 0600 file; never reuse a password.
2. Provision a certificate for the exact hostname (or terminate TLS in a
   reverse proxy); keep the key 0600 (no group/other access).
3. Bind the console to loopback. If a reverse proxy runs on another host, use
   Tailscale/WireGuard or a TLS tunnel rather than exposing the port directly.
4. Restrict egress/ingress to the console port to operators; keep SSO at the
   proxy when one exists.
5. Verify `GET /api/v1/console/healthz` over the final URL before handing out
   the link, and confirm an unauthenticated API request returns `401`.
6. Restart the console after replacing the binary; it keeps no durable state.
