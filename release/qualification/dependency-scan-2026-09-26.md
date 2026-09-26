# ESF v0.5.0 dependency scan, 2026-09-26

This is a local source-dependency check. It does not replace the RC image,
license, provenance, or deployment security reviews.

| Scan | Result |
| --- | --- |
| Frontend `npm audit --audit-level=high` | 0 reported vulnerabilities, including development dependencies. |
| OpenCode package lock `npm audit --audit-level=high` | 0 reported vulnerabilities. |
| `govulncheck ./...` with Go 1.27.1 | 0 reachable vulnerabilities. One package-level report, `GO-2026-6443`, concerns xDS gRPC servers. ESF has no xDS server use. The [gRPC maintainer advisory](https://github.com/grpc/grpc-go/security/advisories/GHSA-2v4p-qf9q-27wj) lists pinned gRPC 1.84.0 as patched, although the Go vulnerability database still flags its imported transport package. |
| `pip-audit --no-deps` on the frozen optional Python lock export | `PYSEC-2026-2447` for transitive `diskcache` 5.6.3. No fixed release is listed in the [PyPA advisory](https://github.com/pypa/advisory-database/blob/main/vulns/diskcache/PYSEC-2026-2447.yaml). The attack requires an adversary who can write the cache directory before a process reads it. |

ESF now disables DSPy's disk cache in both optional Python entry points and
limits each in-memory cache to 1,024 entries. A fresh intake configuration
confirmed that disk caching is off. The lock still contains `diskcache`
because DSPy declares it as a dependency; scans will continue to report the
advisory until upstream removes or fixes it. The optional intake image runs
under UID 10001. A candidate-image scan and verification that every shipped
Python entry point uses the safe cache configuration remain RC gates.

## Local image scan, 2026-09-27

Grype 0.119.0 scanned the locally built images with its refreshed database.
These are scanner matches, not an exploitability assessment. Raw JSON reports
are retained outside the repository in
`/home/mitko/.local/share/esf/upgrades/grype-*-20260927.json`.

| Image | High | Critical | Main finding |
| --- | ---: | ---: | --- |
| Factory runtime | 0 | 0 | No matches. |
| Console runtime | 0 | 0 | No matches. |
| Managed worker | 90 | 29 | Debian 12 packages, including curl, Perl, Expat, and OpenSSH. |
| Optional intake | 64 | 10 | Debian 12 packages, including OpenSSL 3.0.20; the scanner lists 3.0.22 as a fix for several matches. |
| Cube template | 13 | 1 | The critical match is Go 1.25.4 standard library in `/usr/bin/envd` from the Cube base image. |

The managed-worker and intake images use pinned Debian 12-derived bases. The
Cube template inherits CubeSandbox's base image. Refresh the base digests,
rebuild, and rescan; assess any remaining high/critical matches against the
actual binaries and reachable code paths. Do not clear the RC security gate
until the shipped candidate images have no unresolved exploitable findings.
