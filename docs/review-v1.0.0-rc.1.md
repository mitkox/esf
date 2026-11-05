# Independent review — ESF v1.0.0-rc.1

- **Reviewer:** reviewer (independent review workstream; write scope `docs/review-v1.0.0-rc.1.md` only)
- **Date:** 2026-11-05, 12:13 Europe/Sofia
- **Branch:** `release/v1.0.0-rc.1`
- **Candidate:** `ce9625006e180e632e646dc6d11ea8c2c497b15f` (product commit), branch base `051062b47060c684b3bd52a8aeb4e9d958b83333` (v0.7.3)
- **Reviewed tree:** `8f692108efffc338d6a262898d626f77b2682858` (docs/evidence commit) plus the uncommitted post-review evidence fix described below (working tree at review time: 8 modified evidence/docs files, 2 new evidence paths; no product, CI or release-asset changes)
- **Verdict:** **Sign off with caveats** — no open Critical/High blocking findings; the one High finding was verified resolved with fresh evidence. Caveats and pre-publication follow-ups in §6. **Re-confirmed for the final post-resolution tree in §8 (2026-11-05 12:24).**

---

## 1. Scope and method

The review adversarially re-verified the integrated candidate rather than restating its evidence:

1. Read the release plan, `AGENTS.md`, release notes, validation record, ADR 0009, security evidence, and the full `git diff 051062b4..HEAD` (115 files, 115 changed paths) with per-file review of the Harness2 removal, release tooling, workflows, deploy recipes, docs and evidence.
2. Compared removed surface names against the base-commit implementations (`cmd/factory/improvement.go`, `internal/factory/improvement_*.go`, `internal/harnessprofile/`, `internal/inferencebudget/`, config/run-spec structs) and verified the 1.0.0 tree contains no dangling references, flags, config keys, workflow registrations, manifests, SBOM components, archive entries or release-tooling hooks.
3. Independently rebuilt the release set from a detached worktree at `ce962500` (`scripts/release.sh v1.0.0-rc.1`, `scripts/verify-release.sh`, second build, `diff -qr`), compared every digest against `release/qualification/evidence/v1.0.0-rc.1/release-assets.json` and `release/`, inspected archives and the packaged binary, and removed the worktree. The artifacts under `/tmp/esf-rc-dist*` and the review worktrees were deleted after verification.
4. Independently re-ran the available static gates: `go build ./...`, `go vet ./...`, `gofmt -l cmd internal tools`, `make lint`, `python3 scripts/verify-release-inventory.py`, `python3 scripts/verify-pi-inventory.py`, `python3 -m unittest discover -s scripts/tests -p 'test_*.py'`, YAML parse of every workflow/template file, Markdown link resolution, and `gitleaks dir` over the tracked plus untracked non-ignored tree (current working tree, 582 files).
5. Independently re-ran dependency/vulnerability verification where local artifacts and scanners allowed: `grype` 0.121.1 (same DB: built 2026-11-05T06:21:06Z) against every referenced product/platform image; inspected the raw `govulncheck`, OSV, `npm audit`, `pip-audit` and gitleaks outputs; executed the new CI Python-lock audit command locally; re-built the pinned `envd` and Unreal payload stages with `docker build --no-cache` from the pinned inputs and hashed the results; extracted the in-image payloads from the validated template image and hashed them; verified retained source/runtime/test-binary artifacts against the evidence.
6. Ran a base-vs-candidate dead-code comparison (`golang.org/x/tools/cmd/deadcode` over both trees) to prove the removal did not leave newly unreachable production code.
7. Verified the acceptance-evidence defect reported during this review was fixed: the original run's durable manifest recorded `factory_version = 0.7.3`; the re-run on an isolated Temporal task queue records `1.0.0`, is cited in the record/release notes, and the invalid run is retained under `superseded/` as explicitly non-cited history.
8. Confirmed the post-candidate commit `8f692108` contains only the validation record/evidence, release-notes fill and the test-tooling-only `Makefile` fix, and that it changes no shipped artifact (`git diff --stat ce962500..8f692108 -- '*.go' go.mod go.sum deploy .github` is empty; release archives are built from `ce962500`).

Limitations of the method are stated in §7. Host-side project test execution was deliberately avoided per the workspace rules; the Cube-executed suite was reviewed from its evidence and independently spot-verified via artifact hashes.

---

## 2. Verified claims

| # | Claim under review | Result | Evidence |
|---|---|---|---|
| 1 | Harness2 PoC removal is complete: no Go package, CLI command, flag, config key, workflow registration, run-manifest field, deploy overlay, provenance file, documentation page or release tooling entry remains | **VERIFIED** | Deleted paths in `git diff 051062b4..HEAD`; `git grep` over the tracked tree finds only intentional mentions (ADR 0009, 1.0.0 release notes, historical records/plans); rebuilt binary exposes no `improve`, `profile`, `budget-gateway` commands and no `--harness-profile`/`--improvement-policy` flags; `scripts/generate-sbom.py`, `scripts/release.sh`, `scripts/verify-release.sh`, `scripts/verify-release-inventory.py`, `scripts/release_qualification.py` have no Harness2 references; archives contain exactly `LICENSE`, `README.md`, `factory.example.toml`, `factory`; SBOM has 0 Harness2 components |
| 2 | Strict decoders reject the removed configuration and run-spec fields, so retained 0.7.x config/manifests fail loudly | **VERIFIED** | `internal/factory/config.go:335-336` and `cmd/factory/resources.go:674-681` use `DisallowUnknownFields`; regression tests `internal/factory/config_test.go:348-412` (all removed sections/scope keys) and `cmd/factory/resources_test.go:86-117` (TOML+JSON run-spec fields); Cube suite output records `TestLoadConfigRejectsRemovedHarness2Surfaces` PASS (`release/qualification/evidence/v1.0.0-rc.1/cube-test.json`, `cube-test-summary.json`) |
| 3 | Removal does not create dead code or change generic factory behavior | **VERIFIED** | Base-vs-candidate deadcode diff is identical except a line-number shift (`sortedKeys` 627→623); the Temporal replay version marker is retained (`internal/factory/workflow.go:155-156`); `go build`/`vet`/`gofmt`/`make lint` pass; Cube-executed Go suite: 15 binaries, **692 pass records, 0 failures, 0 skips** (sum of `cube-test-summary.json` per-binary counts = 692); no `go.mod`/`go.sum` change |
| 4 | Generic run manifests from earlier releases remain readable | **VERIFIED** | Run manifests are decoded with lenient `json.Unmarshal` (unknown fields ignored); the only strict decoders are config/run-spec inputs. Docs correctly state old manifests are read as-is and that new ones omit the removed fields (`docs/release-v1.0.0-rc.1.md:83-86`) |
| 5 | Version statements are consistent: binary/archive/chart/Python metadata 1.0.0, distribution tag v1.0.0-rc.1 | **VERIFIED** | Rebuilt packaged binary: `release 1.0.0`, commit `ce962500`, config schema 1; `release-manifest.json` `release v1.0.0-rc.1`, `binary_release v1.0.0`; `deploy/helm/esf/Chart.yaml` 1.0.0/1.0.0; `pyproject.toml`/`uv.lock` 1.0.0; `Makefile:13` `ESF_VERSION=1.0.0`; Dockerfile `ARG ESF_VERSION=1.0.0`; release notes state the same split and it matches |
| 6 | `release/inventory.json` is byte-identical to the embedded `internal/releaseinfo/inventory.json`, and pins are coherent with the verifier/Dockerfiles | **VERIFIED** | `cmp` passes; both sha256 `1a6d09b2…436e`; `scripts/verify-release-inventory.py` PASS; `deploy/cube/Dockerfile` sha256 `128757ac…5fec` equals `templates.recipe_sha256`; envd/unreal commit+archive+binary pins appear in the recipe and inventory; intake/runtime/cube base digests appear in the Dockerfiles; `release/inventory.json` no longer has a Harness2 dependency entry |
| 7 | Prerelease handling builds candidate assets like an RC and cannot enter the final-promotion path; no CI gate weakened | **VERIFIED** | `scripts/release.sh:19-26` accepts `test/rc/beta`, refuses final builds; `.github/workflows/release.yml:49,75-83` builds+reproducibility for `-beta.` and skips promotion for rc/beta; `publish` marks any suffixed tag `--prerelease`; `scripts/promote-release.py:27-29` requires a `-rc.N` source; `scripts/release_qualification.py:70-73` accepts only the inventory release or `-rc.N`; `.github/workflows/images.yml:49` accepts beta for candidate images; `ci.yml` only adds the Python audit step (no gate removed/weakened) |
| 8 | Independent RC rebuild at `ce962500` reproduces the recorded assets byte-for-byte | **VERIFIED** | My build produced exactly the evidence digests: linux/amd64 `335a54ea…fc51`, linux/arm64 `3461954c…a7b3`, darwin/amd64 `9a73680c…31d5`, darwin/arm64 `320afec5…a1c3`, manifest `3a785c66…`, dependency inventory `1a6d09b2…`, SBOM `5f42acc0…`; second build `diff -qr` empty; `scripts/verify-release.sh` PASS 6/6; archive contents and packaged binary version/commit verified |
| 9 | SBOM is valid, versioned and Harness2-free and deterministic | **VERIFIED** | Evidence SBOM sha256 `5f42acc0…5ca65`; CycloneDX 1.6, main component `esf` 1.0.0, 328 components, 0 Harness2 components; my independent build's SBOM was byte-identical |
| 10 | Security: zero Critical/High on ESF product artifacts is actually supported by scanner evidence (versions, DB dates, per-artifact before/after) | **VERIFIED** | Evidence `security/security-scan-1.0.0-rc.1.json:9-54` (govulncheck 1.9.0 DB 2026-11-02; grype 0.121.1 DB built 2026-11-05T06:21:06Z; gitleaks 8.30.1; npm 11.20.0; pip-audit 2.10.1) and `:56-168` (per-artifact before/after). I re-scanned the same local image IDs with the same DB: factory 0 matches; intake 5 Medium + 1 Low, **0 C/H**; cube template/full native+Pi **0 C/H** (345 M/46 L/17 N); Temporal server/admin 0 C/H; Temporal UI **1 High** (residual, dev/reference stack); dev-only PostgreSQL **8 Critical/17 High** (residual, dev stack). `govulncheck` raw JSON has 0 findings source+binary; OSV npm: 207 queries (79/13/115), 0 hits; both `pip-audit` snippets report only `diskcache 5.6.3` (Medium, unfixed), matches the table |
| 11 | Rebuilt `envd`/Unreal payload provenance and determinism; digest bindings in recipe/inventory/verifier | **VERIFIED** | Pinned source archives retained with sha256 `0d8523b3…` and `5d734ef8…` (match inventory) and binaries rebuilt from them; my `docker build --no-cache --target envd` and `--target unreal` runs both passed the in-recipe `sha256sum -c` and produced exactly `de39ec5a…4fe1` and `056c3492…8dd9`; payloads extracted from the validated template image `127.0.0.1:32001/esf-native-pi@sha256:bd11873f…` hash to those same digests; recipe sha256 matches inventory and `security/template-build-1.0.0-rc.1.json` |
| 12 | Intake image rewrite is functionally credible and its scan result is credible | **VERIFIED (no functional defect found)** | Final image inspected without execution: pinned Alpine 3.23.7 + uv-managed musl CPython 3.15.2 at `/opt/python`, venv at `/opt/esf/.venv` with real (non-editable) `esf_intake`/`esf_brief_lab` installs and intact `pyvenv.cfg`/symlinks; entrypoint factory unchanged; `make test-python-docker` post-fix log PASS 8+7; grype after = 5 M/1 L, 0 C/H (was 62 High with the Debian base, where apt could not fix) |
| 13 | Secret scan: no leaks in the source tree or committed evidence/logs | **VERIFIED** | Raw gitleaks 8.30.1 report is `[]`; scanner sha256 `88f91962…e5509` equals the local gitleaks 8.30.1 binary; Cube-executed gitleaks over the 538-file candidate tree: no leaks; my gitleaks scan of the final working tree (582 files, tracked+untracked non-ignored, including all new evidence) — no leaks; manual pattern scan of committed evidence/logs found no credentials |
| 14 | Conformance acceptance gate now proves candidate-version execution | **VERIFIED (after fix)** | New durable manifest `.factory/runs/run-8426d021f427ff4240804201/run.json` and `acceptance-run.json:120`: `factory_version = "1.0.0"`; `acceptance.json:20,37-40` documents the unique queue `esf-rc1-validation-20261105` and build HEAD `8f692108` (Go-source-identical to `ce962500`; I re-checked the empty `*.go` diff); worker log records `TaskQueue=esf-rc1-validation-20261105` and the run id; gates all pass, patch sha256 `365194a7…2769`, cleanup verified, zero leaks |
| 15 | Invalidated acceptance run is disclosed, not hidden | **VERIFIED** | `superseded/invalid-run-note.json:7-16` records `factory_version_durable_manifest: 0.7.3`, shared queue and replacement run; invalid manifest/patch/logs retained under `superseded/`; qualification record `:105-149` marks it invalid and cites only the re-run; release notes `:239-243` say it is not cited |
| 16 | Evidence set is internally consistent and checksummed | **VERIFIED (working tree)** | `checksums.json` now has 46 entries; I recomputed all 46 — 0 mismatches, 0 missing files, 0 unlisted files; superseded invalid manifest still records 0.7.3 as history; new `temporal-queue-isolation.log` included |
| 17 | Validation record claims match the evidence | **VERIFIED** | 15 test binaries / 692 records / 0 skip (sum of summary); console 17/17 and untouched dist; Python 8+7; Temporal 6 scenarios; release checks 2/2; gitleaks clean; SBOM 328 components; RC digests; candidate binary version. Acceptance is the only claim that needed the fix and is now correct |
| 18 | Migration notes match the actual removals | **VERIFIED** | Names checked against base code: commands `improve`, `profile` (`import/inspect/qualify/promote/approval-payload`), `budget-gateway` (`digest`), flags `--harness-profile`/`--improvement-policy`, tables `[harness_profiles]/[improvement]/[improvement_policies]/[improvement_suites]`, scope keys, workflow types `ImprovementProbeWorkflow/ImprovementSuiteWorkflow/ImprovementTrialWorkflow`, run-manifest fields, deploy overlay, `third_party/harness2/`, docs/qualification record, companion-manifest section |
| 19 | Links in changed docs resolve; version/security/validation statements are internally consistent | **VERIFIED** | 18 changed/new documents link-checked: 0 broken relative links; security table matches raw scanner evidence; GA gates are explicitly listed as not executed; only minor wording follow-ups remain (§5) |
| 20 | Static gates pass on the reviewed tree | **VERIFIED** | `go build ./...` 0, `go vet ./...` 0, `gofmt -l cmd internal tools` empty, `make lint` 0, release-inventory verifier PASS, Pi-inventory verifier PASS, scripts unittest OK, YAML parse OK for all workflows/templates, gitleaks clean |
| 21 | Post-candidate commit `8f692108` contains only validation record/evidence, release-notes fill, Makefile test-tooling fix; shipped artifacts unchanged; record statements about `ce962500` remain accurate | **VERIFIED** | `git show --stat 8f692108`: 40 files = Makefile + release notes + qualification record/evidence only; no product/CI/release-asset file; `git diff ce962500..8f692108 -- '*.go' go.mod go.sum deploy .github` empty; archives are built from `ce962500` and rebuild matches its digests. Record statements about the candidate are accurate (the acceptance attribution was the one exception and is now corrected in the working tree) |
| 22 | No claim of GA gates that were not executed | **VERIFIED** | Validation record and release notes list soak/Kubernetes/restore-replay/hosted-provider/publication/perf/per-image SBOM/arm64/transitive-license/reviewer sign-off as outstanding; the release-candidate record explicitly says it is not production approval; ACKed accepted caveat (EgressVerified ProbeInconclusive) matches the run conditions |

---

## 3. Findings — Critical

None.

## 4. Findings — High

### R-01 — Acceptance gate cited an execution by a non-candidate binary (0.7.3) — **RESOLVED AFTER REVIEW**

- **Original severity:** High. **Status:** resolved and independently re-verified before sign-off. **Confidence:** 0.97.
- **Defect found initially:** the cited conformance acceptance run `run-0b3447c0f2a7e8220eed42e3` had a durable manifest recording `factory_version = "0.7.3"` (the original `acceptance-run.json:120`, byte-identical to `.factory/runs/run-0b3447c0f2a7e8220eed42e3/run.json`), while `acceptance.json:7` and the validation record/release notes claimed the run validated the 1.0.0 candidate. Candidate source sets `Version = "1.0.0"` and defaults `FactoryVersion` to it with no env/config override, so the executing worker could not have been the candidate build. Root cause reported by the team and consistent with the evidence: stale pre-candidate workers were still polling the shared default Temporal queue `factory`.
- **Fix verified:** the acceptance gate was re-executed on a unique queue. `acceptance-run.json:120` now records `factory_version = "1.0.0"` for `run-8426d021f427ff4240804201`; the run manifest is byte-identical to the durable `.factory/runs/run-8426d021f427ff4240804201/run.json`; `acceptance.json:20` records `task_queue = "esf-rc1-validation-20261105"` and `:37-40` the run/build/version; `logs/acceptance-worker.log:1` records `TaskQueue=esf-rc1-validation-20261105`; the worker log also records the run id on every step; patch sha256 `365194a7…2769` matches; all deterministic gates true; zero leaks; `bin/factory version --json` at run time reported `1.0.0` / commit `8f692108`.
- **Disclosure retained:** the invalid run is preserved under `release/qualification/evidence/v1.0.0-rc.1/superseded/` with `invalid-run-note.json` and is explicitly marked not-cited in the qualification record (`:105-149`), release notes (`:239-243`) and `validation-summary.json` (`:63,98-101`).
- **Residual note for the record:** the re-run used the post-candidate tree `8f692108` (candidate plus tooling/docs/evidence commits). I verified `git diff ce962500..8f692108 -- '*.go' go.mod go.sum deploy .github` is empty, so the executed binary shares the candidate's Go source; the qualification record documents this exception at `:240-244`. This is acceptable and should remain visible in the committed record.

## 5. Findings — Low / Info

### L-01 — Inherited-risk register lacks explicit per-item owner/reachability/GA-gate fields in the *committed* evidence; public GA list omits the arm64/per-image/transitive-license gates (Low, open)

- **Confidence:** 0.9. **Where:** `release/qualification/evidence/v1.0.0-rc.1/security/security-scan-1.0.0-rc.1.json:178-184` and `docs/release-v1.0.0-rc.1.md:164-166` vs `docs/reports/security-1.0.0-rc.1/REPORT.md` (gitignored) residual register.
- The scoping itself is acceptable: Temporal UI 2.58.0 (1 High) and PostgreSQL 18.6-alpine (8 Critical/17 High) are used only by `deployments/dev/temporal/*` (the Helm chart treats Temporal/PostgreSQL as operator-managed dependencies, `deploy/helm/esf/README.md:3-5`), diskcache is a Medium in optional Python tooling with the on-disk DSPy cache disabled in both entry points (`tools/intake/esf_intake/decision.py:41`, `tools/brief_lab/esf_brief_lab/decision.py:244`), the arm64 template gap is pre-existing and explicit, and the plan requirement that product artifacts have zero Critical/High is met for the shipped surfaces. None of these are silently waived.
- However, the committed residual list is plain strings without owner/reachability/GA-gate fields (those exist only in the gitignored `docs/reports/.../REPORT.md` table); it also omits the optional upstream Unreal release binary install path (`scripts/install-agents.sh:26-43`, Go 1.27.0 upstream with the 8 High noted in the before-scan) even though release notes `:165` mention it. The public "Outstanding GA gates" list (`docs/release-v1.0.0-rc.1.md:255-280`) also omits arm64 template qualification, per-image SBOM/vulnerability qualification and transitive-license review that the qualification record (`:230-238`) and the security evidence (`:178-184`) list.
- **Required fix (non-blocking):** either add `owner`, `reachability` and `ga_gate` fields per residual item to the committed security JSON/release notes table, and add the three gates to the public GA list, or state in the release notes that `docs/reports/.../REPORT.md` is the owned residual register and keep it retained with the release. Do this before GA/publication; it does not block the release candidate.

### L-02 — Security-scan provenance is not bound to a candidate commit/tree hash; raw scanner evidence is gitignored; stale file counts (Low, open)

- **Confidence:** 0.85. **Where:** `security/security-scan-1.0.0-rc.1.json:5-6` (`source_commit` is the branch base `051062b4`, `source_state` says "candidate working tree, 74 modified/deleted files"); actual candidate diff is 72 modified/deleted and 538 tracked files; the same file `:51` records a gitleaks scan of 532 tracked files. Raw JSON lives under gitignored `docs/reports/security-1.0.0-rc.1/evidence/raw/` and only the summary JSON is committed.
- The claims are nevertheless substantiated: the Go/npm/Python dependency graphs were unchanged between scan and commit (no `go.mod`/`go.sum`/`package-lock` changes; `uv.lock` only the project version line), the committed tree's gitleaks run is clean (Cube at `ce962500`; my re-scan at HEAD/working tree is clean), the security evidence was captured from a working tree whose image/template digests I independently re-scanned and matched, and the template recipe sha256 in the evidence equals the committed recipe.
- **Required fix (non-blocking):** record the scanned tree's git tree hash (or the exact commit + a diff-vs-candidate statement) and include raw scanner output hashes (or the raw files) in committed evidence; re-state the file counts with their timestamps. This removes the only unverifiable provenance edge in an otherwise well-supported security claim.

### L-03 — Temporal isolation log has a post-hoc chronology: the "before run" check is timestamped after the run (Low, open)

- **Confidence:** 0.95. **Where:** `release/qualification/evidence/v1.0.0-rc.1/logs/temporal-queue-isolation.log:1` and `:32-33` state the check/`before run: none` at `12:11:28`, while the re-run worker started `12:09:13` and finished `12:09:16` (`logs/acceptance-worker.log:1-3`); the log file mtime is 12:11:28.
- The isolation conclusion is still proven by the primary artifact: the worker log records `TaskQueue=esf-rc1-validation-20261105` and the run id on every step, and a uniquely named queue cannot be consumed by a config that does not poll it. The defect is only that the check log is a reconstruction written after the run and its "before run" timestamp is misleading.
- **Required fix (non-blocking):** relabel the log as a post-run reconstruction ("no validation-config worker remains after the run; unique queue only") or re-create the isolation check live before a future acceptance run.

### L-04 — Public release-notes validation preamble still says "against candidate ce962500" while the acceptance gate was re-run from `8f692108` (Low/Info, open)

- **Confidence:** 0.8. **Where:** `docs/release-v1.0.0-rc.1.md:223-226` vs `:239-243`; the qualification record states the exception precisely (`release/qualification/v1.0.0-rc.1.md:240-244`).
- The release-notes bullet now discloses the re-run, and the executed tree shares the candidate's Go source, so this is a wording-precision issue, not a false claim.
- **Required fix (non-blocking):** add one clause to the preamble, e.g. "(all gates on the candidate commit except the isolated acceptance re-run, executed from `8f692108`, whose Go source is identical)".

### I-01 — Information: new CI Python-lock audit step (Info, accepted)

- `ci.yml:126-140` audits the exported locked requirements with `pip-audit==2.10.1`, narrowly ignoring `PYSEC-2026-2447`/`GHSA-w8v5-vhqr-4h9v` (the same diskcache advisory, whose vulnerable path is disabled). I ran the exact commands locally: export succeeds and `pip-audit` reports "No known vulnerabilities found, 2 ignored". The step is a new gate, not a weakened one.
- Minor robustness note: the shell step does not set `pipefail`; add `set -euo pipefail` so a partially failed `uv export` cannot feed a truncated requirements file to the audit.

### I-02 — Information: intake image packaging nits (Info, accepted)

- `deploy/images/Dockerfile.intake:27-47` intentionally ships the uv-managed interpreter and the venv only (tool sources/tests stay in the builder). I confirmed the installed `esf_intake`/`esf_brief_lab` are real installs in `site-packages` (not editable path references), so the final image has no dangling path. The uv-managed CPython directory carries harmless leftover `.temp/`, `.lock`, `.gitignore` entries; PATH contains a non-existent `/opt/python/bin` entry that is shadowed by the venv. No functional issue for the shipped entrypoint; worth cleaning up for image tidiness.

### I-03 — Information: source-archive creation command is not documented (Info, accepted)

- The record cites the tar sha256 `dc936ad9…` and 538 files. I found the retained archive at `/tmp/esf-source-ce962500.tar.gz`, verified its sha256, and verified its path set and contents are identical to `git ls-files` at `ce962500` — so the claim is substantiated. However, the exact deterministic packing command is not recorded, so an external reviewer cannot regenerate the same hash. Recommend documenting the packing command alongside the evidence (a reproducibility nicety, not a claim defect).

## 6. Sign-off

**Verdict: sign off with caveats.** The release candidate's shipped artifacts and the claims made for this release candidate are verified to the extent possible from this workspace:

- No open Critical or High blocking findings remain. The one High finding raised during review (acceptance evidence attributed to a non-candidate 0.7.3 worker) is **resolved**: the re-run records `factory_version = 1.0.0`, used an isolated Temporal queue, and the invalid run is retained and explicitly not cited.
- The candidate's Harness2 removal is complete, decoders reject retained 0.7.x configuration/run-specs, generic factory behavior is unchanged, and the removal creates no new dead code.
- The release assets reproduce byte-for-byte from `ce962500` against the recorded digests, the SBOM is deterministic and Harness2-free, and the inventory/embedded inventory/pins/verifiers are coherent.
- The zero-Critical/High claim for ESF product artifacts is supported by scanner versions/DB dates/per-artifact evidence and independently reproduced by re-scans; inherited residuals are scoped correctly as non-product/dev-only/upstream.
- The payload rebuild provenance (`envd`, Unreal) reproduces from pinned inputs, and the shipped template contains exactly those pinned binaries.

Caveats to carry into the commit/GA:

1. **Commit exactly the reviewed content.** The review covers the working tree with the acceptance fix applied; `checksums.json` currently verifies 46/46 entries and `gitleaks` is clean. If any evidence byte changes after this review, re-run the checksum/gitleaks verification.
2. Track L-01/L-02 (residual-risk owner/GA metadata, scan-tree provenance/raw evidence) as pre-publication or GA-gate follow-ups; they do not block this release candidate but should not age into GA.
3. Fix L-03/L-04 wording/chronology before publication.
4. Publication remains a separate gate; this review does not assert that the release candidate has been published or that any GA gate (soak, Kubernetes/Helm, restore/replay, hosted-provider acceptance, image/template publication, load/performance, per-image SBOM/vulnerability qualification, arm64, transitive licenses) has been executed.

---

## 7. Residual risks and limits of this review

**Residual risks carried by the release candidate (not defects of this review):**

- Temporal UI 2.58.0: 1 upstream High (GO-2026-5026) in the dev/reference stack; derived patched image proven locally (0/0) but not adopted — operator/GA gate.
- Dev-only PostgreSQL 18.6-alpine: 8 Critical/17 High upstream, dev/reference stack only; derived patched image proven (0/0) but not adopted — dev-stack GA gate.
- `diskcache` 5.6.3 Medium, unfixed upstream; vulnerable on-disk cache path disabled in both DSPy entry points; CI ignores the advisory narrowly.
- Cube template is `linux/amd64` only; arm64 template qualification remains a GA gate.
- Optional upstream Unreal release binary (Go 1.27.0) remains for the operator install path outside the rebuilt template; optional and documented, but the residual register should carry owner/GA gate (L-01).
- Cube vendor base 2026.16 is the latest tag; rebuilt payloads remove its Critical/High, but vendor-layer Medium/Low/Negligible matches remain and grype matches versions, not reachability.
- Scanner results are a point-in-time snapshot (grype DB 2026-11-05T06:21:06Z); re-run before publication.

**Limits of this review:**

- Per the workspace rules, project test suites were not executed on the host. Cube/runtime gates were reviewed from retained evidence and independently spot-verified via artifact hashes (source archive, python runtime, 15 test binaries, image contents); the Cube suite itself was not re-executed in this review.
- The factory/intake container images were not rebuilt from scratch; I re-scanned the exact local image IDs recorded in the evidence (identical `imageID`/`manifestDigest`) and inspected the intake image filesystem. The template payload stages were rebuilt no-cache from pinned inputs and matched, but a full end-to-end template/image build was not repeated.
- Raw scanner JSON and the detailed security report are gitignored (`docs/reports/security-1.0.0-rc.1/`); I inspected those local files, but external reviewers can only re-derive the committed summary. This is L-02.
- Upstream source archives were not re-fetched from the network by this review; their sha256 values were verified against retained copies and their builds were reproduced. The mapping from those bytes to the upstream tag rests on the recorded pins.
- I did not re-run the Cube/Temporal runtime checks (`cube-smoke`, `factory doctor`, `agents verify`, acceptance), the console npm tests/build, or `make test-python`; those are single-sourced to retained logs and the Cube-executed suite. The acceptance defect found in this review shows why such single-sourced evidence needs cross-checking — it is now cross-checked.
- Review performed against the uncommitted working-tree state described at the top; the final commit was made by the Lead after this review. Any post-review edit invalidates the checksum-based statements unless re-verified.

---

## Appendix — Commands run and results (reviewer, 2026-11-05)

| Command / action | Result |
|---|---|
| `git diff 051062b4..HEAD`, `git show --stat 8f692108`, per-file diff review | reviewed |
| `go build ./...`; `go vet ./...`; `gofmt -l cmd internal tools` | exit 0; exit 0; empty |
| `make lint` | exit 0 (gofmt/vet/shellcheck) |
| `python3 scripts/verify-release-inventory.py` | PASS ("release inventory pins match lockfiles") |
| `python3 scripts/verify-pi-inventory.py` | PASS |
| `python3 -m unittest discover -s scripts/tests -p 'test_*.py'` | OK (1 test) |
| YAML parse of all `.github/workflows/*.yml`, dependabot, templates, Helm values | all OK |
| Markdown link check over 18 changed docs | 0 broken |
| `gitleaks dir` over working tree (582 files, tracked+untracked non-ignored) | no leaks (exit 0) |
| Detached worktree at `ce962500`; `scripts/release.sh v1.0.0-rc.1`; `scripts/verify-release.sh`; second build; `diff -qr` | PASS; 6/6 checksums; no diff; all 7 evidence digests matched |
| Rebuilt binary `factory version --json` | release 1.0.0, commit ce962500, go1.27.3, schema 1 |
| CLI surface of rebuilt binary (`--help`, `improve/profile/budget-gateway`, removed flags) | commands/flags absent as documented |
| `grype` 0.121.1 (cached evidence DB) re-scan of factory, intake, cube-template, native-pi, temporal server/admin/UI, postgres | factory 0; intake 5M/1L (0 C/H); template/full 0 C/H; server/admin 0 C/H; UI 1H; postgres 8C/17H — matches evidence |
| `docker build --no-cache --target envd` and `--target unreal` from pinned inputs; hash outputs | `de39ec5a…4fe1` and `056c3492…8dd9` — match pins; in-build checksum checks passed |
| Extract `/usr/bin/envd` and `/opt/esf/agents/unreal-agent-runner` from `127.0.0.1:32001/esf-native-pi@sha256:bd11873f…`; sha256 | match pins |
| Verify retained `envd`/Unreal source-tarball sha256; source tree tar `dc936ad9…` (hash + path/content equality vs `git ls-files` at `ce962500`); python runtime `37a7c68a…`; 15 test-binary hashes | all match evidence |
| deadcode over base `051062b4` and candidate `ce962500` trees | identical except one line-number shift; no newly dead code from removal |
| `pip-audit` 2.10.1 simulation of the new CI step (`uv export … | pip-audit … --ignore-vuln PYSEC-2026-2447 --ignore-vuln GHSA-w8v5-vhqr-4h9v`) | exit 0, "No known vulnerabilities found, 2 ignored" |
| Evidence checksum re-verification (46 entries) | 0 mismatches, 0 missing, 0 unlisted |
| Verify acceptance fix: new run manifest, worker log queue, superseded retention, docs updates | all verified as described in R-01 |
| Worktree/dist cleanup | review worktrees and RC dists removed; `git worktree list` clean of review paths |

---

## 8. Post-review resolution (2026-11-05 12:24, appended after Lead remediation)

Line numbers in §1–§7 refer to the initially reviewed tree; citations in §8 refer to the current post-resolution tree.

After the initial sign-off the Lead reported that L-01 to L-04 and the CI Info item had been addressed. I re-verified the current working tree (HEAD `8f692108` plus uncommitted docs/evidence/CI changes; no product or release-asset changes) and re-confirmed the verdict.

### 8.1 Resolution status

| ID | Original finding | Status | Resolution evidence (current tree) | Reviewer re-verification |
|---|---|---|---|---|
| R-01 | Acceptance gate attributed to a non-candidate (0.7.3) worker | **RESOLVED** (already verified before initial sign-off) | `acceptance-run.json:120` `factory_version 1.0.0`; `logs/acceptance-worker.log:1` unique queue; `superseded/invalid-run-note.json`; record §"Conformance acceptance" | Unchanged and still valid |
| L-01 | Committed inherited-risk register lacked owner/reachability/GA-gate fields; public GA list omitted arm64/per-image/license gates | **RESOLVED** | `docs/release-v1.0.0-rc.1.md:179-188` new "Residual inherited-platform risks" table with Finding/Reachability/Owner/GA gate for Temporal UI, dev PostgreSQL, diskcache, arm64 (`:186`), optional upstream Unreal binary, per-image SBOM/license; GA list (`:268-295`) now includes per-image SBOM/transitive-license evidence (`:284-287`) and "Final release review" (`:290-292`); tense fixed at `:6` | Read and matched against the unchanged scanner residual list; operatorship/reachability wording consistent with the Helm chart (operator-managed Temporal/PostgreSQL) and the diskcache-disabled code paths verified earlier |
| L-02 | Security-scan provenance not bound to a commit/tree; raw evidence gitignored; stale counts | **RESOLVED** | `security/security-scan-1.0.0-rc.1.json:11-147`: candidate tree `40bbe05e…` (verified = `git rev-parse ce962500^{tree}`), 538 entries / 537 regular / 1 symlink (`factory.example.toml`), scan-time 533 entries with 5 added + 3 updated files reconciled, exact-tree gitleaks report `37517e5f…b570` (two identical runs, 0 leaks), scanned-tree manifest `bca89f2e…5648` (538 entries/58 368 bytes), raw-evidence manifest `27939a14…76bc` (42 files/4 230 bytes), rebuilt-image non-regression (`b0294cae…`, `cc463178…`); new JSON sha256 `52c25931…784b` matches `validation-summary.json` | **Independently reproduced:** candidate tree hash and 538/537/1 tree shape; exact-tree `git archive ce962500` gitleaks reports byte-identical across two runs and sha256 exactly `37517e5f…`; canonical scanned-tree manifest exactly `bca89f2e…` (538/58 368); raw-evidence manifest exactly `27939a14…` (42/4 230); rebuilt image IDs exist locally and my grype rescan gives factory 0 matches and intake 5 Medium/1 Low (0 C/H); deploy input hashes `128757ac…`/`e93cda5a…`/`8402689c…` match; `uv export` of the committed lock is byte-identical to the scanned 3.15 closure; 47/47 evidence checksums verify (0 mismatch/missing/unlisted) |
| L-03 | Isolation log was a post-hoc reconstruction with a misleading "before run" timestamp | **RESOLVED** | `logs/temporal-queue-isolation-note.json` declares the log a post-run reconstruction and names the primary proofs; record `:120-130` cites the note; `checksums.json` includes the note | Verified note content; primary proof remains the worker log `TaskQueue=esf-rc1-validation-20261105` plus durable manifest `factory_version = 1.0.0` |
| L-04 | Public validation preamble said "against candidate ce962500" while acceptance was re-run from `8f692108` | **RESOLVED** | Release notes `:250-256` now state the re-run used evidence commit `8f692108` with Go source identical to `ce962500` | Diff read; `git diff ce962500..8f692108 -- '*.go' go.mod go.sum deploy .github` is empty |
| I-01 | CI Python-audit step lacked `pipefail` | **RESOLVED** | `.github/workflows/ci.yml:132` now begins the audit step with `set -euo pipefail` | Diff read; no gate removal/weakening |
| I-02/I-03 | Intake image tidiness; source-tar packing command undocumented | **NOT CHANGED (accepted Info)** | — | Not release-blocking; remained Info in §5 |

### 8.2 Final-tree re-verification

- `checksums.json`: **47/47 entries** valid, 0 mismatch/missing/unlisted; includes the new security-scan hash and the isolation note.
- Security evidence: scanner versions/DB dates unchanged (govulncheck 1.9.0 / 2026-11-02, grype 0.121.1 / DB 2026-11-05T06:21:06Z, gitleaks 8.30.1, npm 11.20.0, pip-audit 2.10.1); every `product`/`product-payload` artifact row remains 0 Critical/High; residual inherited findings unchanged (Temporal UI 1 High, dev PostgreSQL 8C/17H, diskcache Medium, arm64, vendor base) and now carry owner/GA-gate rows in the release notes.
- `gitleaks` over the final working tree (584 tracked+untracked non-ignored files): **no leaks**.
- No product or release-asset change since the review: `git diff` over `*.go`, `go.mod/go.sum`, `deploy/`, `release/inventory.json`, `internal/releaseinfo/inventory.json` and `scripts/` is empty; the only non-docs/evidence change is the CI `pipefail` hardening; RC assets remain built from `ce962500` and my earlier rebuild/digest comparisons still apply.
- Record and references: qualification record now has an independent-review gate row (`:49`) and section (`:218-231`) linking `docs/review-v1.0.0-rc.1.md`, a security-provenance paragraph (`:203-210`), and a final-release-review GA gate; `validation-summary.json` carries `independent_review`, `security_evidence` provenance and acceptance execution blocks.

### 8.3 Re-confirmed verdict

**Sign off with caveats — still valid for the final post-resolution tree.** No open Critical/High findings; the earlier High evidence-integrity finding remains resolved, and the Low/Info items that were previously follow-ups are now either resolved in this tree or explicitly accepted as informational. Remaining caveats:

1. **Commit exactly the reviewed bytes.** Any post-review change to evidence bytes invalidates the 47-entry checksum/gitleaks statements and must be re-verified.
2. **L-02 residual limit:** the raw scanner JSON and detailed security report remain gitignored; the provenance manifests hash that local evidence, but external reviewers can only read the committed summary plus hashes. This is now explicit and auditable, not a hidden waiver.
3. **Not re-executed by me in this addendum:** the evidence's govulncheck/npm-audit non-regression runs and the 3.14 Python closure export (I verified the 3.15 export is byte-identical and inspected the unchanged raw scanner outputs). The scanner data/versions are unchanged from the initial review.
4. **Info items remain accepted:** intake image packaging nits and the undocumented source-tar packing command.
5. **GA gates unchanged:** a final-release independent review, plus all soak/Kubernetes/restore-replay/hosted-provider/publication/perf/per-image-SBOM/arm64/transitive-license gates, remain outstanding for 1.0.0 GA; this review does not assert publication or GA qualification. All inherited platform residuals remain as recorded with owner and GA gate.
