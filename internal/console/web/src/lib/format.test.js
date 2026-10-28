import assert from "node:assert/strict";
import test from "node:test";
import {
  cleanupTone,
  formatBytes,
  formatDateTime,
  formatDelta,
  formatDuration,
  formatNumber,
  formatPercent,
  formatUsd,
  initials,
  isInflight,
  parseUnifiedDiff,
  patchSummaryFromText,
  percentile,
  repoLabel,
  runStateLabel,
  runStateTone,
  shortSha,
  timeAgo,
  truncate,
  windowLabel,
} from "./format.js";

test("formatNumber uses separators and compact notation", () => {
  assert.equal(formatNumber(1234567), "1,234,567");
  assert.equal(formatNumber(1200, { compact: true }), "1.2K");
  assert.equal(formatNumber(null), "—");
});

test("formatPercent and formatDelta are stable for dashboards", () => {
  assert.equal(formatPercent(0.7726), "77%");
  assert.equal(formatPercent(0.074, { digits: 1 }), "7.4%");
  assert.equal(formatPercent(null), "—");
  assert.equal(formatDelta(12.34), "+12.3pp");
  assert.equal(formatDelta(-1.2), "-1.2pp");
  assert.equal(formatDelta(null), "");
});

test("formatDuration covers milliseconds to days", () => {
  assert.equal(formatDuration(950), "950ms");
  assert.equal(formatDuration(1500), "1.5s");
  assert.equal(formatDuration(90000), "1m 30s");
  assert.equal(formatDuration(3 * 60 * 60 * 1000), "3h");
  assert.equal(formatDuration(26 * 60 * 60 * 1000), "1d 2h");
  assert.equal(formatDuration(null), "—");
});

test("formatBytes and formatUsd report absence honestly", () => {
  assert.equal(formatBytes(512), "512 B");
  assert.equal(formatBytes(2048), "2.0 KiB");
  assert.equal(formatBytes(undefined), "—");
  assert.equal(formatUsd(null), "Not reported");
  assert.equal(formatUsd(0), "$0.00");
  assert.equal(formatUsd(0.0012), "$0.0012");
});

test("time formatting helpers are deterministic", () => {
  const reference = Date.parse("2026-10-27T12:00:00Z");
  assert.equal(timeAgo("2026-10-27T11:59:30Z", reference), "just now");
  assert.equal(timeAgo("2026-10-27T11:30:00Z", reference), "30m ago");
  assert.equal(timeAgo("2026-10-27T09:00:00Z", reference), "3h ago");
  assert.equal(timeAgo("2026-10-20T12:00:00Z", reference), "7d ago");
  assert.equal(timeAgo(""), "—");
  assert.equal(shortSha("abcdef1234567890"), "abcdef1");
  assert.match(formatDateTime("2026-10-27T12:34:00Z"), /Oct 27, 2026/);
  assert.equal(formatDateTime(null), "—");
});

test("run state helpers classify outcomes", () => {
  assert.equal(runStateLabel("SUCCEEDED"), "Succeeded");
  assert.equal(runStateLabel("VERIFICATION_FAILED"), "Verification failed");
  assert.equal(runStateTone("SUCCEEDED"), "success");
  assert.equal(runStateTone("QUARANTINED"), "security");
  assert.equal(runStateTone("VERIFICATION_FAILED"), "danger");
  assert.equal(runStateTone("RUNNING"), "info");
  assert.equal(isInflight("RUNNING"), true);
  assert.equal(isInflight("PAUSED"), true);
  assert.equal(isInflight("SUCCEEDED"), false);
});

test("cleanup and repository presentation", () => {
  assert.equal(cleanupTone({ outcome: "SUCCESS", verified: true }), "success");
  assert.equal(cleanupTone({ outcome: "SUCCESS", verified: false }), "danger");
  assert.equal(cleanupTone(null), "muted");
  assert.equal(repoLabel("https://github.com/acme/api.git"), "github.com/acme/api");
  assert.equal(repoLabel("local:/home/ops/repos/tooling"), "repos/tooling");
  assert.equal(repoLabel(""), "Unknown repository");
  assert.equal(windowLabel("7d"), "Last 7 days");
  assert.equal(initials("Factory Operator"), "FO");
  assert.equal(truncate("abcdef", 4), "abc…");
});

test("percentile interpolates and handles empty input", () => {
  assert.equal(percentile([], 0.5), null);
  assert.equal(percentile([10], 0.95), 10);
  assert.equal(percentile([1, 2, 3, 4, 5], 0.5), 3);
  assert.equal(percentile([1, 2, 3, 4], 0.5), 2.5);
});

test("parseUnifiedDiff produces per-file rows and stats", () => {
  const diff = [
    "diff --git a/app/main.go b/app/main.go",
    "index 1111111..2222222 100644",
    "--- a/app/main.go",
    "+++ b/app/main.go",
    "@@ -1,3 +1,4 @@",
    " package main",
    "-import \"fmt\"",
    "+import \"errors\"",
    "+import \"fmt\"",
  ].join("\n");
  const parsed = parseUnifiedDiff(diff);
  assert.equal(parsed.files.length, 1);
  assert.equal(parsed.files[0].path, "app/main.go");
  assert.equal(parsed.additions, 2);
  assert.equal(parsed.deletions, 1);
  assert.deepEqual(parsed.files[0].lines.map((line) => line.type), ["hunk", "context", "del", "add", "add"]);

  const summary = patchSummaryFromText(diff);
  assert.equal(summary.additions, 2);
  assert.equal(summary.deletions, 1);
  assert.equal(summary.files[0].path, "app/main.go");
});

test("parseUnifiedDiff detects binary patches", () => {
  const diff = [
    "diff --git a/logo.png b/logo.png",
    "index 1111111..2222222 100644",
    "Binary files a/logo.png and b/logo.png differ",
  ].join("\n");
  const parsed = parseUnifiedDiff(diff);
  assert.equal(parsed.binary, true);
  assert.equal(parsed.files.length, 1);
});

test("parseUnifiedDiff keeps changed lines that look like file headers", () => {
  const diff = [
    "diff --git a/scripts/run.sh b/scripts/run.sh",
    "index 1111111..2222222 100644",
    "--- a/scripts/run.sh",
    "+++ b/scripts/run.sh",
    "@@ -1 +1 @@",
    "--- counter;", // removed line whose content starts with "-- "
    "+++ counter;", // added line whose content starts with "++ "
  ].join("\n");
  const parsed = parseUnifiedDiff(diff);
  assert.equal(parsed.additions, 1);
  assert.equal(parsed.deletions, 1);
  const types = parsed.files[0].lines.map((line) => line.type);
  assert.deepEqual(types, ["hunk", "del", "add"]);
  assert.equal(parsed.files[0].lines[1].text, "-- counter;");
  assert.equal(parsed.files[0].lines[2].text, "++ counter;");
});
