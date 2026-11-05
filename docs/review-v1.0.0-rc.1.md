# Independent review — ESF v1.0.0-rc.1

- **Reviewer:** reviewer (independent review workstream; write scope `docs/review-v1.0.0-rc.1.md` only)
- **Date:** 2026-11-05 16:25 Europe/Sofia
- **Branch:** `release/v1.0.0-rc.1`
- **Candidate:** `1c98bc34dc1a81a1a64e563ffe12bc13549e5442` (`release: v1.0.0-rc.1`), parent `156101568d6512af7f36921ee92ee551da7ab00f`, release content based on `051062b47060c684b3bd52a8aeb4e9d958b83333`
- **Reviewed tree:** `1c98bc34` (git tree `470b58c8e696d16b5f2966aec319096f6a4cb786`, 585 entries) plus the uncommitted regenerated qualification evidence and updated release notes/record. No product, dependency, deploy-instruction or release-asset change is pending in the working tree.
- **Context:** the 1.0.0 prerelease was retroactively renamed from `beta.1` to `rc.1`. The product content is unchanged from the audited candidate; the relabel commit changes documentation/labels/evidence paths, one Cube-recipe comment and the bound recipe SHA-256 only. The beta-era review findings are therefore retained as history in §5 and the rc.1 re-verification is the authoritative record below.
- **Verdict:** **Sign off with caveats** — no open Critical/High findings; zero Critical/High on ESF product artifacts is re-verified for the rc.1 tree and regenerated evidence. Caveats in §7.

---

## 1. Scope and method

The rc.1 relabel and regenerated qualification evidence were verified independently rather than re-read from the record:

1. Reviewed the relabel commit (`15610156..1c98bc34`) file-by-file, confirming label-only changes: documentation/evidence path renames and label rewrites, workflow/issue-template example strings, one Cube-recipe comment, and the matching recipe SHA-256 in the release inventories. Verified no Go code, `go.mod`/`go.sum`, `uv.lock`/`pyproject.toml`, npm lockfile, deploy image, Helm template, deployment script or release-tooling behaviour changed (`git diff --stat ce962500 1c98bc34 -- '*.go' go.mod go.sum uv.lock pyproject.toml agents/*/package-lock.json internal/console/web/package-lock.json deploy/images deploy/helm/esf/templates deployments scripts cmd tools` is empty).
2. Independently rebuilt the release set from a detached clean worktree at `1c98bc34` with `scripts/release.sh v1.0.0-rc.1`, `scripts/verify-release.sh`, a second build and `diff -qr`, and compared every digest with `release/qualification/evidence/v1.0.0-rc.1/release-assets.json` and the evidence `release/` files. The worktree and `/tmp` dists were removed after verification.
3. Independently reproduced the security provenance bindings: candidate tree hash, exact-tree gitleaks report (two runs), canonical scanned-tree manifest, raw-evidence manifest, the retained source archive hash/content, and the in-template payload digests extracted from the recorded image.
4. Re-ran the fast static gates and documentation checks: `go build ./...`, `go vet ./...`, `gofmt -l cmd internal tools`, `make lint`, `scripts/verify-release-inventory.py`, `scripts/verify-pi-inventory.py`, scripts unittest discovery, YAML parse of every workflow/template, Markdown link resolution, and `gitleaks` over the tracked+untracked working tree and over the evidence directory.
5. Re-scanned the two re-verified rebuilt images recorded in the rc.1 security JSON (`esf/factory:rc1-verify`, `esf/intake:rc1-verify`) with grype 0.121.1 and the same DB, and extracted the `envd`/Unreal payloads from the validated template image to re-check their pinned digests.
6. Scanned the whole tracked tree for unintended prerelease wording and classified each occurrence against the allowed exceptions.
7. Checked qualification-record and release-notes claims against the regenerated evidence (counts, run IDs, digests, template identity, checksums, GA gates, residual register).

---

## 2. Relabel scope verification

| Check | Result |
|---|---|
| Commit contains only label/doc/evidence changes plus one recipe comment and the bound recipe SHA-256 | **VERIFIED.** 69 changed paths: doc/evidence renames, release-note/record/review labels, workflow and issue-template example strings, `deploy/cube/Dockerfile` comment line, `deploy/helm/esf/README.md` title/link, `deploy/cube/README.md` title, and `templates.recipe_sha256` in both release inventories. |
| No product/build behaviour change | **VERIFIED.** `git diff --stat ce962500 1c98bc34 -- '*.go' go.mod go.sum uv.lock pyproject.toml … deploy/images deployments scripts cmd tools` is empty. |
| Recipe SHA updated only for the comment; no build instruction or pin changed | **VERIFIED.** `sha256sum deploy/cube/Dockerfile` = `c4c91267…99eb`, equal to `release/inventory.json`, `internal/releaseinfo/inventory.json` and the security template-build evidence. `scripts/verify-release-inventory.py` PASS. |
| Image digests stand; template payloads unchanged | **VERIFIED.** The validated full template image `127.0.0.1:32001/esf-native-pi@sha256:bd11873f…` is present locally; its `/usr/bin/envd` and `/opt/esf/agents/unreal-agent-runner` hash exactly `de39ec5a…4fe1` and `056c3492…8dd9` (the pins), so the relabelled recipe still describes the same payloads. |
| Inventory/embedded inventory byte-identical | **VERIFIED.** Both sha256 `47a545358414dafa5df933c4f455f9e6906b5658622e9ebae8145e8a08c754c9`; `cmp` passes. |
| Beta.1 evidence/record tree removed, rc.1 names self-contained | **VERIFIED.** `release/qualification/evidence/v1.0.0-rc.1/` exists with no beta references; the beta.1 evidence tree, beta.1 qualification record and beta.1 release notes/review paths are gone from the tracked tree; no tracked link still points at a beta.1 path. |

---

## 3. Verified claims (rc.1 final tree + regenerated evidence)

| # | Claim | Result | Evidence |
|---|---|---|---|
| 1 | Version statements are consistent: binary 1.0.0, distribution tag v1.0.0-rc.1 | **VERIFIED** | Rebuilt packaged binary reports release `1.0.0`, commit `1c98bc34`, schema 1; `release-manifest.json` release `v1.0.0-rc.1`, binary_release `v1.0.0`, commit `1c98bc34`; Chart 1.0.0/1.0.0; `pyproject.toml`/`uv.lock` 1.0.0; release notes `:9-13` state the same split |
| 2 | Inventory pins coherent; result bound to the rc.1 tree | **VERIFIED** | `release/inventory.json` release `v1.0.0`, recipe sha `c4c91267…`; embedded copy byte-identical; verifier PASS; every envd/Unreal/base-image pin appears in the recipe and inventory |
| 3 | Prerelease tooling accepts `-rc.N` and cannot enter the final-promotion path; no gate weakened | **VERIFIED (unchanged from audited candidate)** | `scripts/release.sh:19-26` prerelease path; `.github/workflows/release.yml:49,75-83` builds rc assets and excludes them from promotion; `images.yml:7,49`; `promote-release.py:27-29` requires an RC source; `release_qualification.py:70-73` accepts only final or `-rc.N` |
| 4 | Independent RC build at `1c98bc34` reproduces the recorded assets byte-for-byte | **VERIFIED** | My build produced exactly: linux/amd64 `ee3f5738…8277`, linux/arm64 `0c4a4fdd…55e8`, darwin/amd64 `ff0da53f…7e64`, darwin/arm64 `2eb1fb5e…429f`, manifest `fd015ff3…32b1`, inventory `47a54535…54c9`, SBOM `5f42acc0…5a65`; `verify-release.sh` PASS 6/6; second build `diff -qr` empty |
| 5 | Archives contain exactly `LICENSE`, `README.md`, `factory.example.toml`, `factory`; packaged binary version/commit correct | **VERIFIED** | My rebuild and the evidence `release-assets.json:24-27`; extracted linux/amd64 binary reports release 1.0.0/commit `1c98bc34`/go1.27.3/schema 1 |
| 6 | SBOM valid, deterministic and Harness2-free | **VERIFIED** | Evidence SBOM sha256 `5f42acc0…5a65`; CycloneDX 1.6, main component 1.0.0, 328 components, 0 Harness2 components; my independent build's SBOM byte-identical |
| 7 | Zero Critical/High on ESF product artifacts | **VERIFIED** | Security JSON sha256 `ef57a3df…3dbc`; every `product`/`product-payload` artifact row after = 0/0; scanner versions/DB unchanged (govulncheck 1.9.0 / 2026-11-02; grype 0.121.1 / DB 2026-11-05T06:21:06Z; npm 11.20.0; pip-audit 2.10.1; gitleaks 8.30.1). My grype rescan of the recorded re-verified images: factory `66d082b8…` 0 matches; intake `97c8b217…` 5 Medium + 1 Low, 0 Critical/High. Template/full image rows 0/0 |
| 8 | Security evidence is bound to the committed rc.1 tree | **VERIFIED** | `security-scan-1.0.0-rc.1.json:13-143`: candidate tree `470b58c8…` (verified = `git rev-parse 1c98bc34^{tree}`), 585 entries (584 regular + 1 symlink `factory.example.toml`), parent/base diff, relabel scope; my reproductions below |
| 9 | Exact-tree gitleaks report and scanned-tree/raw-evidence manifests are reproducible | **VERIFIED** | Two `gitleaks 8.30.1` runs on `git archive 1c98bc34` produced byte-identical reports with sha256 exactly `37517e5f…b570` (0 leaks); canonical scanned-tree manifest reproduced exactly `6eccf959…d6976` (585 entries / 64 718 bytes); raw-evidence manifest reproduced exactly `c44a5b44…2969` (49 files / 4 868 bytes); scanner binary sha256 `88f91962…e5509` matches local gitleaks 8.30.1 |
| 10 | Retained source archive executed in Cube is the committed tree | **VERIFIED** | `/tmp/esf-rc1-source.tar.gz` sha256 exactly `86e834ac…58b9`, 585 entries; extracting it and `git archive 1c98bc34` differ only in the three final security-evidence JSON working-tree edits, exactly as the record states (`release/qualification/v1.0.0-rc.1.md:35-39`) |
| 11 | Cube-executed suite results | **VERIFIED from evidence** | `cube-test-summary.json`: 15 binaries, sum of `passed` = **692**, 0 fail, 0 skip, checks 2/2 exit 0, sandbox `fddf6292…`, source commit marker `1c98bc34`, cleanup verified, remaining 0; `cube-test.json` source sha256 `86e834ac…`, gitleaks report `[]` "no leaks found", python runtime sha `37a7c68a…` |
| 12 | Conformance acceptance proves candidate-version execution | **VERIFIED** | `acceptance.json:4-22` candidate/build = `1c98bc34` itself (working tree differed only by three unstaged security JSON files); run `run-44042c2526941b3e1bfc70f5` on isolated queue `esf-rc1-validation-20261105`; `acceptance-run.json` is byte-identical to the durable run manifest and records `factory_version = "1.0.0"`; all deterministic gates true; patch sha256 `365194a7…2769` matches the run's `changes.patch`; cleanup verified, remaining 0 |
| 13 | Release-assets evidence is internally consistent | **VERIFIED** | `release-assets.json:4-58`; evidence `release/` manifests/checksums/SBOM hash-verified; `release/checksums.txt` matches my rebuilt archives; dependency inventory identical to the frozen inventory |
| 14 | Qualification record and release notes match the evidence | **VERIFIED** | Counts (15/692/0/0, 17/17, 8+7, 6 scenarios, 328 components), run IDs, template ID + image digest, checksums, security shas, GA gates and residual register all match the files I verified; no unexecuted gate is claimed |
| 15 | Evidence checksums are complete and correct | **VERIFIED** | `checksums.json` has **41 entries**, 0 mismatch, 0 missing, 0 unlisted; the 42 evidence files include `checksums.json` itself; `logs/gitleaks-evidence.log` and the isolation log/note are checksummed |
| 16 | Static checks pass on the rc.1 tree | **VERIFIED** | `go build ./...`, `go vet ./...`, `gofmt -l` clean, `make lint`, release-inventory verifier, Pi-inventory verifier, scripts unittest, YAML parse (13 files), all PASS |
| 17 | Documentation links resolve and the rc.1 paths are wired | **VERIFIED** | 15 changed/current documents link-checked: 0 broken; release notes `:289-291` and qualification record `:212-219` link `docs/review-v1.0.0-rc.1.md`; no tracked link points at the removed beta.1 paths |
| 18 | No unintended prerelease wording in product docs/evidence | **VERIFIED** (with one internal Info note) | Committed rc.1 evidence tree contains **0** "beta" occurrences; tracked occurrences are confined to allowed exceptions listed in §6, plus this review document's historical references |
| 19 | Working tree has no product change pending | **VERIFIED** | The uncommitted paths are the regenerated qualification evidence, `release/qualification/v1.0.0-rc.1.md` and `docs/release-v1.0.0-rc.1.md` (38 paths at review time, before this review document was written); `git diff` over Go/deps/deploy-instructions/scripts/tooling is empty, and the commit's only non-documentation edits are human-readable example strings in the release/images workflow inputs and issue template, the Cube-recipe label comment and its bound recipe SHA-256. RC assets remain those built from `1c98bc34` |
| 20 | No open Critical/High; GA gates not overclaimed | **VERIFIED** | Security verdict true with only inherited platform residuals (Temporal UI 1 High, dev PostgreSQL 8C/17H, diskcache Medium, arm64, vendor base); release notes `:267-294` and qualification record `:221-236` list the unexecuted GA gates and a final-release independent review |

---

## 4. Findings

### Critical / High / Medium

None open for the rc.1 tree.

### History: prior findings from the audited prerelease

| ID | Prior finding (beta-era review) | rc.1 status |
|---|---|---|
| R-01 (High) | The originally cited conformance acceptance run had been executed by a stale 0.7.3 worker on the shared queue, so its durable manifest recorded a non-candidate factory version while the record claimed a candidate run | **Resolved and not applicable to rc.1.** The beta evidence tree was regenerated/removed with the rename; rc.1 has a fresh acceptance run (`run-44042c25…`) executed on the candidate commit itself on an isolated queue, durable manifest `factory_version = 1.0.0`, and no superseded-run material remains in the rc.1 evidence tree. The stale host workers still exist and were again deliberately left running; the unique queue isolates the gate (`logs/temporal-queue-isolation-note.json` records the primary proofs) |
| L-01 (Low) | Committed inherited-risk register lacked owner/reachability/GA-gate fields; public GA list omitted arm64/per-image/license gates | **Resolved.** rc.1 release notes `:179-188` carry the owner/reachability/GA-gate table; GA list `:267-294` includes per-image SBOM/transitive-license and final-release review |
| L-02 (Low) | Security-scan provenance was not bound to a commit/tree; raw evidence gitignored; stale counts | **Resolved and independently reproduced** (see §3 rows 8-10 and 15) |
| L-03 (Low) | Isolation log was a post-hoc reconstruction with a misleading "before run" timestamp | **Resolved.** `logs/temporal-queue-isolation-note.json` states the provenance and the primary proofs; record `:143-147` cites it |
| L-04 (Low) | Public validation preamble said "against candidate" while a gate used a post-candidate build | **Resolved by construction.** All rc.1 gates ran against `1c98bc34` (acceptance executed build = candidate commit; only unstaged security-evidence JSONs differed, not build inputs), and the record states this |
| I-01 (Info) | CI Python-audit step lacked `pipefail` | **Resolved.** `.github/workflows/ci.yml:132` still starts the audit step with `set -euo pipefail` |
| I-02/I-03 (Info) | Intake-image packaging nits; source-tar packing command undocumented | **Accepted Info.** Intake image contents are unchanged from the audited candidate; the rc.1 record now documents the source archive entries/hash and I verified the retained archive, but the exact packing command remains undocumented |

---

## 5. Security claims not re-executed in this pass

The scanner outputs for the product surfaces are unchanged from the audited candidate and are bound to the rc.1 tree by the provenance block. I re-executed the checks that could be reproduced locally (exact-tree gitleaks, provenance manifests, image rescans of the two recorded re-verified builds, in-image payload hashes, source-archive hash/content) but did not rebuild the factory/intake images or re-run govulncheck/npm-audit/pip-audit from scratch; those raw results are retained and date-stamped. This is a review limit, not a claim of new evidence.

---

## 6. Prerelease-wording scan

`git grep -i beta` over the tracked tree yields 217 occurrences, all classified as allowed exceptions:

- **Functional prerelease-format support (allowed):** `.github/workflows/release.yml:49,51,60,75,78,83`, `.github/workflows/images.yml:7,49`, `scripts/release.sh:19` — the tooling intentionally accepts `rc` **and** `beta` formats so a beta-form prerelease remains buildable/verifiable; no product behaviour depends on it.
- **Vendored agent strings (allowed):** `agents/pi/dist/pi-runner.mjs` (OpenAI SDK `OpenAI-Beta` headers/APIs) and `internal/agentharness/pi-catalog.json` (`openrouter/auto-beta` catalog entry).
- **Historical evidence (allowed):** `release/qualification/evidence/live-upgrade-2026-10-15/native-proxy-audit.json` records `?beta=true` endpoints from that historical run.
- **Historical review context (allowed):** this review document references the beta.1 review history; it is clearly labelled history.

No occurrences were found in the committed rc.1 evidence tree, the rc.1 release notes/record, or any current product documentation.

**Info residual (not committed):** the gitignored local security report `docs/reports/security-1.0.0-rc.1/REPORT.md` still uses the beta.1 title/paths, and the point-in-time raw grype JSON inputs retain the beta-era image tag names (`userInput`) for images whose digests are unchanged. These are local, gitignored raw artifacts (the committed security summary has zero beta references and the raw-evidence manifest hashes the files); relabeling the internal report for consistency is recommended but not required for the commit.

---

## 7. Residual risks, limits and sign-off

### Residual risks carried by the release candidate

- Temporal UI 2.58.0: 1 upstream High (GO-2026-5026), dev/reference stack only; derived patched image proven 0/0 but not adopted — operator/GA gate.
- Dev-only PostgreSQL 18.6-alpine: 8 Critical/17 High upstream, dev/reference stack only; derived patched image proven 0/0 but not adopted — dev-stack GA gate.
- `diskcache` 5.6.3 Medium, unfixed upstream; vulnerable on-disk cache path disabled; CI keeps a narrow documented ignore.
- Cube template is linux/amd64; arm64 template qualification remains a GA gate.
- Optional upstream Unreal release binary (Go 1.27.0, 8 High in the upstream install path) remains for operators who install outside the rebuilt template — recorded with owner/GA gate in the rc.1 release notes.
- Cube vendor base 2026.16 is the latest tag; vendor-layer Medium/Low matches remain and grype matches versions, not reachability.
- Scanner evidence is a point-in-time snapshot (grype DB 2026-11-05T06:21:06Z); re-run before publication.
- The beta.1 tag/release still exists locally and must be deleted/superseded as planned; ensure no beta.1 assets are published after the rename.

### Limits of this review

- Project test suites were not executed on the host; Cube/runtime gates are reviewed from retained evidence and independently spot-verified via artifact hashes (source archive, python runtime, 15 test binaries, template payloads, re-verified images).
- The factory/intake images were not rebuilt in this pass; I re-scanned the exact image IDs recorded in the rc.1 security evidence and extracted the template payloads. The RC archives were rebuilt from scratch at `1c98bc34` and matched all recorded digests.
- Raw scanner JSON and the detailed security report are gitignored; I inspected and reproduced the manifests/hashes, but external reviewers only see the committed summary.
- Upstream source archives were not re-fetched from the network; their sha256 values match the retained copies used for the reproducible payload builds.
- The working tree contains the final regenerated evidence uncommitted at review time; the verdict applies to exactly the reviewed bytes. Any later evidence change invalidates the 41-entry checksum and gitleaks statements.

### Verdict

**Sign off with caveats — valid for the rc.1 tree.** No open Critical/High or Medium findings; the relabel is label-only and does not change product behaviour; the RC builds reproducibly from `1c98bc34`; the security provenance is bound to the committed tree and independently reproduced; the acceptance gate is executed by the candidate build itself; and the prior beta-era findings are resolved or no longer applicable. Caveats:

1. **Commit exactly the reviewed content.** The regenerated evidence currently verifies 41/41 checksums and gitleaks-clean; if any evidence byte changes before commit, re-run the checksum/gitleaks verification.
2. **Delete/retire the superseded beta.1 tag/release/evidence as planned** and confirm nothing beta.1-labelled is published or retained as current; the rc.1 evidence is self-contained and the committed tree no longer references beta.1 paths.
3. **Info follow-ups:** relabel the gitignored internal `docs/reports/security-1.0.0-rc.1/REPORT.md`; document the source-archive packing command; tidy the intake image's leftover uv metadata (all non-blocking). A final-release independent review and the listed soak/Kubernetes/restore-replay/hosted-provider/publication/load/per-image-SBOM/arm64/transitive-license gates remain outstanding for 1.0.0 GA.

---

## Appendix — commands run and results (rc.1 re-verification)

| Command / action | Result |
|---|---|
| `git show 1c98bc34` / `git diff 15610156..1c98bc34` review; code/dependency/deploy diff against `ce962500` | label-only; no functional diff |
| `sha256sum deploy/cube/Dockerfile` vs inventory/evidence | `c4c91267…99eb` — match |
| Detached worktree at `1c98bc34`; `scripts/release.sh v1.0.0-rc.1`; `scripts/verify-release.sh`; repeat build; `diff -qr` | PASS; 6/6 checksums; no diff; all 7 evidence digests matched |
| Rebuilt binary `factory version --json` | release 1.0.0, commit `1c98bc34`, go1.27.3, schema 1 |
| `git rev-parse 1c98bc34^{tree}`; tree shape vs evidence | `470b58c8…b786`; 585 entries (584 regular + 1 symlink) — match |
| Exact-tree gitleaks (two runs) / canonical scanned-tree manifest / raw-evidence manifest | `37517e5f…b570` (0 leaks, identical), `6eccf959…d6976` (585/64 718), `c44a5b44…2969` (49/4 868) — all reproduced |
| Retained source archive hash/content vs committed tree | `86e834ac…58b9`, 585 entries; differs only in the three final security JSON working-tree edits — matches record |
| Template image payload extraction (`envd`, Unreal) | `de39ec5a…4fe1`, `056c3492…8dd9` — match pins |
| grype 0.121.1 rescans of `esf/factory:rc1-verify` / `esf/intake:rc1-verify` | 0 matches / 5 Medium + 1 Low (0 Critical/High) — matches evidence |
| Evidence checksums | 41/41 valid, 0 mismatch/missing/unlisted |
| `go build ./...`; `go vet ./...`; `gofmt -l`; `make lint`; verifiers; scripts unittest; YAML parse | all PASS |
| Markdown link check over current docs/record/release notes (15 files) | 0 broken |
| `gitleaks dir` over working tree (578 files) and over the evidence dir | no leaks |
| Prerelease-wording scan | only allowed exceptions (§6); committed rc.1 evidence 0 occurrences |
| Cleanup | independent worktree and `/tmp` dists removed; `git worktree list` clean of review paths |
