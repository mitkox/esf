# v0.5.0 benchmark record

Local measurements on 26 September 2026 use the pinned Go 1.27.1 and Vite
8.3.1 toolchains. The release archives came from `v0.5.0-test6`; they are
unqualified dry-run artifacts. Sizes are bytes unless stated otherwise.

| Item | Measured | Method |
| --- | ---: | --- |
| Linux/amd64 factory binary | 31,711,392 | Extracted stripped binary |
| Linux/amd64 Machinist binary | 13,578,400 | Extracted stripped binary |
| Linux/amd64 factory archive | 11,332,713 | `esf_factory` tar.gz |
| Linux/amd64 combined archive | 17,142,266 | `esf_combined` tar.gz |
| Factory archive saving | 33.9% | Relative to combined archive; target ≥25% |
| Initial frontend JavaScript, integrated unoptimized | 97.71 kB gzip | Vite build output recorded before route splitting |
| Initial frontend JavaScript, current | 77.03 kB gzip | Vite build output after route splitting |
| Initial frontend JavaScript reduction | 21.2% | Target ≥20%; remeasure from frozen RC inputs |
| Factory runtime image | 12,246,262 | Local `docker image inspect .Size` |
| Console runtime image | 6,619,613 | Local `docker image inspect .Size` |
| Managed-worker image | 70,083,856 | Local `docker image inspect .Size` |
| Optional intake image | 150,640,413 | Local `docker image inspect .Size` |
| Cube template image | 167,806,125 | Local `docker image inspect .Size`; not imported into a live Cube cluster |

## Local VM concurrency fixture

`scripts/benchmark-factory.py` ran the deterministic `testdata/e2e-repo`
fixture at revision `c73b463deadf2f30216635cf9d3c0672526685e2` on the same
host against CubeSandbox 0.7.2 and Temporal 1.32.0. Each trial asked the
conformance agent to change `hello` to `hello factory`; independent `build.sh`
and `test.sh` gates checked the patch. The old-template comparison used the
same factory binary, fixture, and host, but installed Git, CA certificates,
and pip during each run. The pinned template contains those tools already.

| Concurrent runs | Template | Batch wall time | P95 repository ready | P95 run duration | Worker peak RSS | Evidence per run |
| ---: | --- | ---: | ---: | ---: | ---: | ---: |
| 1 | Old | 10.12 s | 9.22 s | 9.92 s | 47.9 MB | 9.7 kB |
| 1 | Pinned | 1.30 s | 0.43 s | 1.10 s | 46.7 MB | 9.7 kB |
| 4 | Old | 16.95 s | 15.86 s | 16.77 s | 54.3 MB | 9.7 kB |
| 4 | Pinned | 2.21 s | 0.53 s | 1.96 s | 55.9 MB | 9.7 kB |
| 8 | Old | 21.97 s | 20.96 s | 21.70 s | 58.4 MB | 9.7 kB |
| 8 | Pinned | 2.31 s | 0.72 s | 2.06 s | 63.9 MB | 9.7 kB |

All 26 runs passed verification with one agent attempt and verified cleanup;
no factory-owned sandboxes remained after any batch. P95 uses nearest rank
within each 1-, 4-, or 8-run batch, so these are preliminary measurements.
The old template is a preparation comparison, not a pre-optimization ESF
version. Cube RSS, Kubernetes runs, output-limit behavior, repeated samples,
the 24-hour soak, and the full regression gate remain open. The production
release verifier therefore keeps the performance and soak gates closed.
