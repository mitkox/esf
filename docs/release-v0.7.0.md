# ESF 0.7.0 enterprise console and production TLS candidate

0.7.0 makes the factory console a first-class product surface and promotes it
to an HTTPS-capable, single-host production deployment. It keeps the 0.6.2
component, agent and security pins; it does not change the `factory.toml`
schema or the durable evidence layout.

## Changes

- New `factory console` command embedding a React console in the factory
  binary. The console is read-only over the durable artifact store:
  Overview, Dispatch, Runs, Changes, Reviews, Security, Operations and Settings.
  It never submits a run, approves a review or cancels a workflow; those stay
  in the authenticated CLI, with copyable command affordances in the UI.
- Native HTTPS (`--tls-cert-file`/`--tls-key-file`), TLS 1.2+ floor, HTTP/2,
  HSTS, and refusal of world-readable private keys. The console remains
  loopback-only by default and supports a TLS reverse proxy instead.
- Brute-force protection: failed bearer attempts are throttled per client
  (10 failures / 5 minutes, one-minute lockout) so an exposed console cannot be
  token-guessed indefinitely.
- Evidence index, overview, runs, changes, review queue, security aggregate,
  operations and redacted configuration views; optional bounded live Temporal
  and CubeSandbox reads; gzip responses.
- Version, release inventory, Helm chart, image build args and operator docs
  move to 0.7.0.

The inherited Machinist CLI/control plane/web UI removal from the 0.6.2 branch
is retained: the factory and its console are the only product surfaces.

## Upgrade notes

1. Build or install the 0.7.0 archive/image for the exact commit to be run.
2. Existing `factory.toml` files are compatible; no migration is required.
   Evidence under `[storage] data_dir` is read as-is.
3. Generate a console read token (for example `openssl rand -hex 32`) in a
   private 0600 file. The console refuses to serve without one unless
   `--no-auth` is used on a loopback listener for local evaluation.
4. For native TLS, provision a certificate/key pair, keep the key 0600, and
   pass both flags (or `FACTORY_CONSOLE_TLS_CERT_FILE` /
   `FACTORY_CONSOLE_TLS_KEY_FILE`). Otherwise terminate TLS in a reverse proxy
   in front of the loopback console.
5. Restart the Temporal worker and the console after replacing the binary. The
   console holds no durable state; restarting it is safe while runs execute.

## Validation boundary

The candidate requires the same attested companion qualification manifest,
hosted-provider checks and deployment matrix as earlier releases. Local
validation for this change covers:

- Go build/vet and test-binary compilation; frontend `npm ci`/build
  reproducibility; release-inventory verification.
- Independent cross-checks of console aggregates against the artifact store.
- Browser-level console checks (authentication, navigation, keyboard, mobile,
  contrast/clipping/overflow) and TLS transport checks (HTTP/2, TLS 1.2 floor,
  HSTS, invalid-key refusal, auth throttling).
- An independent scoped code review; findings are recorded in the console
  plan at `docs/plans/2026-10-27-enterprise-factory-console.md`.

Unit suites are authoritative in CI (Linux and macOS) rather than on the
factory host. No Cube execution path changed; the console creates no sandbox.
Publishing tags, building qualified images and production approval remain
separate operator gates.
