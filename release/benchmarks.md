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

The deterministic live fixture is the `testdata/e2e-repo` tree
`a9145c45dc5434d97ecaa67fa404da4d286aa41a`. Each trial asks the same
pinned agent to change the greeting to `hello factory` and runs `build.sh` and
`test.sh` as independent gates. Run isolated batches at 1, 4, and 8 concurrent
factory submissions, with the same Cube template, Temporal cluster, agent
model, and egress policy on both VM and Kubernetes. Record p50/p95 sandbox
preparation, wall time, peak factory and Cube RSS, output-limit behavior,
retained evidence bytes per run, and unreconciled sandbox count. Compare the
same fixture and hardware to the pre-optimization baseline; investigate any
regression above 10%.

Live preparation, RSS, evidence retention, 1/4/8 concurrency, and 24-hour soak
measurements are pending. The production release verifier therefore keeps the
performance and soak gates closed.
