# Component upgrade validation, 2026-10-27

The candidate starts from GitHub master
`ba829056ab728b8e002dddcb702edc0be5b19a1d` and preserves the unrelated dirty
primary checkout. Official upstream metadata is retained in
[the audit](evidence/component-upgrade-2026-10-27-upstream.json).

| Component | Previous | Candidate |
|---|---|---|
| Python | 3.15.1 | 3.15.2 |
| uv | 0.13.5 | 0.13.6 |
| Lucide React | 1.61.0 | 1.62.0 |
| Claude CLI | 2.1.310 | 2.1.311 |
| modernc libc | 1.79.0 | 1.79.1 |
| Google API/RPC generated modules | 20261019-ce908d29299b | 20261026-2ffab32bce7e |
| Python boto3 / botocore | 1.43.118 | 1.43.119 |
| Python cachetools | 7.2.4 | 7.2.6 |
| Frontend PostCSS | 8.5.31 | 8.5.32 |

The Pi lock also refreshes compatible AWS SDK and Smithy dependencies. Its
rebuilt runner, model catalog and notices remain byte-identical; its lock digest
is refreshed. The OpenCode graph remains unchanged after refresh and audit.

Go 1.27.3 and PostgreSQL 18.6-alpine images have refreshed immutable digests;
the Python image advances to 3.15.2-slim. Node retains its existing slim image
variant. Other audited direct npm/Go dependencies, Go/Node toolchains, Cube,
Temporal, Unreal, Codex, Helm, Kubernetes and scanners remain at current releases.
Historical release reports retain their original observations.

## Cube validation

Project execution used disposable local Cube microVMs. The primary validation
used sandbox `5ed6e6f703404a1cbdd49016e5c673be`; Python 3.15.2 validation used
`6310ba34789742a8ad935851ee20b77e`.

- Full `go test ./...`, `go vet ./...` and module verification passed.
- `govulncheck` found no reachable vulnerabilities.
- Frontend: 40 tests passed, production build passed, npm audit found zero
  vulnerabilities. Rebuilt tracked frontend assets remain byte-identical.
- Pi: 17 tests and regenerated build/catalog passed; Pi and OpenCode npm audits
  found zero vulnerabilities.
- Python 3.15.2 with uv 0.13.6: lock check, frozen installation, dependency
  compatibility, intake 8 tests and brief lab 7 tests passed. Native orjson was
  rebuilt using explicitly pinned Rust 1.99.1 and recorded C compiler packages.
- The same Python lock passed 15 tests on supported Python 3.14.9. An initial
  Python 3.15.1 attempt lacked a C compiler; the independent 3.15.2 validation
  supplied the required compiler and passed.
- Release/Pi inventory checks, three release qualification tests, six Temporal
  schema safety scenarios and six issue triage tests passed.
- Source secret scanning found no leaks. Claude's official release archive and
  extracted binary digests were verified; `--version` returned 2.1.311 in Cube.

Independent review found one stale Docker Python assertion, which was corrected
to 3.15.2. This review does not grant human approval.
Both owned validation sandboxes were destroyed and final factory sandbox count
was zero. Final synchronized inventories, SBOM validation and secret scan passed.

## Local system

The existing managed-tool updater completed successfully. Global Python was
advanced to 3.15.2; uv is 0.13.6 and Claude is 2.1.311. Local Temporal UI was
upgraded from 2.56.0 to 2.57.0, with HTTP 200 verified. PostgreSQL remains 18.6
on its refreshed image and is healthy. Its data volume was retained and a
pre-upgrade database backup remains local. Temporal server remains healthy at
1.32.1; the deployed Cube proxy/lifecycle manager already use 0.7.3.

The operator completed the requested Ubuntu metadata refresh and package upgrade
in their authenticated terminal. Subsequent `apt-get -s upgrade` reported zero
eligible upgrades and nine packages deferred by Ubuntu's phased rollout. No
reboot was required; deferred packages were left under the distribution policy.

## Remaining qualification

Image/template builds, model-backed acceptance, eligible human approval, full
OpenCyber AI triage, production deployment, replay, restore, soak and performance
qualification remain separate gates. Local health checks and passing unit tests
do not satisfy them. GitHub CI reports its own results after publication.
