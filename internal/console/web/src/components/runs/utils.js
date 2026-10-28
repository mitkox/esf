// Shared presentation helpers for the runs ledger and the run evidence viewer.
//
// The list and the detail screen must agree about status language, pagination
// defaults and evidence semantics, so both import from here instead of keeping
// private copies. Nothing in this module invents data: missing evidence is
// reported as missing, never as a clean or zero result.

import { hrefFor } from "@/routes";
import {
  formatDuration,
  formatNumber,
  repoLabel,
  runStateLabel,
  runStateTone,
  titleCase,
} from "@/lib/format";

export const DEFAULT_PAGE_SIZE = 25;
export const PAGE_SIZES = [25, 50, 100];
export const DEFAULT_SORT = "-started_at";

// Server sort keys (see internal/console/handlers.go sortRuns): a leading "-"
// sorts descending, "+" ascending, no prefix defaults to descending.
export const SORT_OPTIONS = [
  { value: "-started_at", label: "Newest first" },
  { value: "+started_at", label: "Oldest first" },
  { value: "-completed_at", label: "Recently completed" },
  { value: "-duration", label: "Longest duration" },
  { value: "+duration", label: "Shortest duration" },
  { value: "+run_id", label: "Run ID A → Z" },
  { value: "-run_id", label: "Run ID Z → A" },
];

const OUTCOMES = {
  SUCCESS: { label: "Success", tone: "success" },
  SUCCEEDED: { label: "Succeeded", tone: "success" },
  PASSED: { label: "Passed", tone: "success" },
  APPROVED: { label: "Approved", tone: "success" },
  FAILED: { label: "Failed", tone: "danger" },
  ERROR: { label: "Error", tone: "danger" },
  REJECTED: { label: "Rejected", tone: "danger" },
  TIMED_OUT: { label: "Timed out", tone: "warning" },
  PENDING: { label: "Pending", tone: "warning" },
  CANCELLED: { label: "Cancelled", tone: "muted" },
  SKIPPED: { label: "Skipped", tone: "muted" },
  UNKNOWN: { label: "Unknown", tone: "muted" },
};

export function outcomeMeta(value) {
  if (value === null || value === undefined || value === "") {
    return { label: "Not reported", tone: "muted" };
  }
  const key = String(value).toUpperCase();
  return OUTCOMES[key] || { label: titleCase(value), tone: "muted" };
}

export function cleanupMeta(cleanup) {
  if (!cleanup || !cleanup.outcome) {
    return { tone: "muted", label: "Not reported", detail: "No cleanup evidence was recorded for this run." };
  }
  const remaining = Number(cleanup.remaining_sandboxes) || 0;
  if (cleanup.outcome === "SUCCESS" && cleanup.verified) {
    return {
      tone: "success",
      label: "Verified",
      detail: `Cleanup verified; ${remaining} sandbox${remaining === 1 ? "" : "es"} remaining.`,
    };
  }
  if (cleanup.outcome === "SUCCESS") {
    return { tone: "warning", label: "Unverified", detail: "Cleanup reported success but was not verified." };
  }
  if (cleanup.outcome === "SKIPPED") {
    return { tone: "warning", label: "Skipped", detail: cleanup.error || "Sandbox cleanup was skipped." };
  }
  return {
    tone: "danger",
    label: titleCase(cleanup.outcome),
    detail: cleanup.error || `Cleanup failed; ${remaining} sandbox${remaining === 1 ? "" : "es"} remaining.`,
  };
}

export function securityMeta(hardening, runState = "") {
  // Security-terminal run states outrank the per-control summary: a run can be
  // stopped by an unverifiable egress posture even when the behavior scan and
  // finding counters are empty.
  const state = String(runState || "").toUpperCase();
  if (state === "GATE_TAMPERED") {
    return { tone: "danger", label: "Gate tampered", detail: "The patch modified a deterministic gate program." };
  }
  if (state === "EGRESS_UNVERIFIED") {
    return {
      tone: "danger",
      label: "Egress unverified",
      detail: "The egress probe could not verify the network boundary; the run is a security stop.",
    };
  }
  if (state === "QUARANTINED") {
    return { tone: "danger", label: "Quarantined", detail: "The run was quarantined by a hardening control." };
  }
  if (!hardening || !hardening.measured) {
    return {
      tone: "muted",
      label: "Not measured",
      detail: "No hardening evidence is recorded for this run; this is not a clean result.",
    };
  }
  if (hardening.behavior_measured !== true && !hardening.tripped) {
    return {
      tone: "warning",
      label: "Behavior not measured",
      detail:
        "Other hardening controls ran, but the behavior scan is absent (the monitor may be disabled). This is not a clean scan.",
    };
  }
  const findings = Number(hardening.findings) || 0;
  const egressViolations = Number(hardening.egress_violations) || 0;
  const modifiedGates = Array.isArray(hardening.gate_modified) ? hardening.gate_modified.length : 0;
  if (hardening.tripped) {
    return { tone: "danger", label: "Tripped", detail: "A hardening rule tripped and the run failed closed." };
  }
  if (egressViolations > 0) {
    return {
      tone: "danger",
      label: `${egressViolations} egress violation${egressViolations === 1 ? "" : "s"}`,
      detail: "The egress probe proved at least one network invariant false.",
    };
  }
  if (findings > 0) {
    return {
      tone: "warning",
      label: `${findings} finding${findings === 1 ? "" : "s"}`,
      detail: hardening.max_severity ? `Maximum severity: ${titleCase(hardening.max_severity)}.` : "Behavior scan recorded findings.",
    };
  }
  if (modifiedGates > 0) {
    return { tone: "security", label: "Gate modified", detail: "The patch modified a deterministic gate program." };
  }
  if (!hardening.gate_checked) {
    return { tone: "warning", label: "Gate not checked", detail: "Behavior scan is clean, but gate integrity was not measured." };
  }
  return { tone: "success", label: "Clean", detail: "No behavior findings, egress violations or gate modifications recorded." };
}

export function reviewMeta(run) {
  if (!run) return { tone: "muted", label: "Not reported", detail: "" };
  const outcome = String(run.review_outcome || "").trim().toLowerCase();
  if (outcome) {
    if (["approved", "approve", "passed"].includes(outcome)) {
      return { tone: "success", label: "Approved", detail: "A human reviewer approved this run." };
    }
    if (["rejected", "reject", "denied"].includes(outcome)) {
      return { tone: "danger", label: "Rejected", detail: "A human reviewer rejected this run." };
    }
    if (["timeout", "timed_out", "expired"].includes(outcome)) {
      return { tone: "warning", label: "Review timed out", detail: "The review gate expired without a decision." };
    }
    if (["pending", "awaiting", "awaiting_review"].includes(outcome)) {
      return { tone: "warning", label: "Awaiting review", detail: "A human decision is required before this run completes." };
    }
    return { tone: "info", label: titleCase(outcome), detail: `Recorded review outcome: ${outcome}.` };
  }
  if (run.factory_result === "PAUSED") {
    return { tone: "warning", label: "Awaiting review", detail: "The run is paused at the human review gate." };
  }
  if (run.review_configured) {
    return { tone: "info", label: "Configured", detail: "A review gate is configured for this run." };
  }
  return { tone: "muted", label: "Not required", detail: "No human review gate was configured." };
}

export function isReviewPending(run) {
  if (!run) return false;
  if (run.factory_result === "PAUSED") return true;
  return /pending|awaiting/i.test(String(run.review_outcome || ""));
}

// time.Duration marshals as an integer nanosecond count on the Go side.
export function durationNS(value) {
  const number = Number(value);
  if (!Number.isFinite(number)) return null;
  return formatDuration(number / 1e6);
}

const ARTIFACT_KIND_LABELS = {
  text: "Text",
  json: "JSON",
  log: "Log",
  patch: "Patch",
  other: "Other",
};

const ARTIFACT_KIND_VARIANTS = {
  text: "outline",
  json: "info",
  log: "muted",
  patch: "primary",
  other: "outline",
};

export function artifactKindLabel(kind) {
  return ARTIFACT_KIND_LABELS[kind] || titleCase(kind || "Other");
}

export function artifactKindVariant(kind) {
  return ARTIFACT_KIND_VARIANTS[kind] || "outline";
}

export function runHref(runID) {
  return hrefFor("run", runID);
}

export function changeHref(changeID) {
  return hrefFor("change", changeID);
}

// Trigger a client-side download without a server round trip. The object URL is
// revoked on the next tick so Chromium has captured it before cleanup.
export function downloadText(filename, text) {
  const blob = new Blob([text ?? ""], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.style.display = "none";
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

export function describeAuditDetail(detail) {
  if (detail === null || detail === undefined) return "";
  if (typeof detail !== "object") return String(detail);
  const parts = [];
  for (const [key, value] of Object.entries(detail)) {
    if (value === null || value === undefined || value === "") continue;
    parts.push(`${key}=${typeof value === "string" ? value : JSON.stringify(value)}`);
  }
  return parts.join(" · ");
}

export function sumFacetCounts(facets) {
  return (facets || []).reduce((total, facet) => total + (Number(facet.count) || 0), 0);
}

function sortFacets(facetValues) {
  return [...(facetValues || [])].sort(
    (left, right) => (Number(right.count) || 0) - (Number(left.count) || 0) || String(left.value).localeCompare(String(right.value)),
  );
}

export function stateFacetOptions(facetValues = [], selected = []) {
  const entries = sortFacets(facetValues);
  for (const value of selected) {
    if (!entries.some((entry) => entry.value === value)) entries.push({ value, count: null });
  }
  return entries.map((entry) => ({
    value: entry.value,
    label: runStateLabel(entry.value),
    count: entry.count,
    tone: runStateTone(entry.value),
  }));
}

// buildSelectOptions keeps the selected value visible even when the current
// search narrowed the facet list so the control can never show an empty box.
export function buildSelectOptions(facetValues = [], selected = "", allLabel, mapLabel = (value) => value) {
  const entries = sortFacets(facetValues);
  if (selected && !entries.some((entry) => entry.value === selected)) {
    entries.push({ value: selected, count: null });
  }
  return [
    { value: "", label: allLabel },
    ...entries.map((entry) => ({
      value: entry.value,
      label: entry.count === null || entry.count === undefined
        ? mapLabel(entry.value)
        : `${mapLabel(entry.value)} · ${formatNumber(entry.count)}`,
    })),
  ];
}

// repoLabel collapses local paths to their last two segments, which can make
// the demo fixture and the checkout fixture look identical. When that happens,
// add one more path segment so the two selectable repositories stay distinct.
function distinguishingRepoLabel(value) {
  const raw = String(value || "");
  if (raw.startsWith("local:")) {
    const path = raw.slice("local:".length).replace(/\/+$/, "");
    const parts = path.split("/").filter(Boolean);
    const take = path.startsWith("/") ? 3 : 2;
    const label = parts.slice(-take).join("/");
    if (label) return label;
  }
  return repoLabel(raw);
}

export function repositorySelectOptions(facetValues = [], selected = "") {
  const entries = sortFacets(facetValues);
  if (selected && !entries.some((entry) => entry.value === selected)) {
    entries.push({ value: selected, count: null });
  }
  const baseLabels = entries.map((entry) => repoLabel(entry.value));
  const labelCounts = baseLabels.reduce((counts, label) => counts.set(label, (counts.get(label) || 0) + 1), new Map());
  return [
    { value: "", label: "All repositories" },
    ...entries.map((entry, index) => {
      const base = baseLabels[index];
      const label = labelCounts.get(base) > 1 ? distinguishingRepoLabel(entry.value) : base;
      return {
        value: entry.value,
        label: entry.count === null || entry.count === undefined ? label : `${label} · ${formatNumber(entry.count)}`,
      };
    }),
  ];
}
