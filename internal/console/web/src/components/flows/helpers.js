// Derivation helpers shared by the change portfolio, change detail and review
// queue screens.
//
// Every label below is derived from payload fields. Nothing is estimated: when
// the artifact store did not report tokens or cost, the helpers return an
// explicit "Not reported" label instead of a zero.

import { formatNumber, formatUsd } from "@/lib/format";

export const CHANGE_STATUS_ORDER = ["OPEN", "IN_REVIEW", "DONE", "ABANDONED"];

function facetList(facets, key) {
  const list = facets?.[key];
  return Array.isArray(list) ? list : [];
}

// facets are `{value, count}` arrays on the wire; screens want lookups.
export function facetMap(facets, key) {
  const map = new Map();
  for (const entry of facetList(facets, key)) {
    const value = entry && typeof entry.value === "string" ? entry.value : "";
    if (!value) continue;
    map.set(value, Number(entry.count) || 0);
  }
  return map;
}

export function facetOptions(facets, key) {
  return facetList(facets, key)
    .filter((entry) => entry && typeof entry.value === "string" && entry.value)
    .map((entry) => ({ value: entry.value, count: Number(entry.count) || 0 }))
    .sort((left, right) => right.count - left.count || left.value.localeCompare(right.value));
}

// Review state in the portfolio table is derived from the durable change
// status only. The list payload does not carry a review outcome, so the label
// describes the decision state without claiming an outcome.
export function reviewStateMeta(change) {
  switch (change?.status) {
    case "IN_REVIEW":
      return { label: "Awaiting decision", tone: "warning", hint: "This change is waiting in the human review queue." };
    case "DONE":
      return {
        label: "Resolved",
        tone: "success",
        hint: "The change reached its terminal approved state; the list payload does not report the review outcome.",
      };
    case "OPEN":
      return { label: "No decision pending", tone: "muted", hint: "The change is not waiting for a human decision." };
    case "ABANDONED":
      return { label: "No decision pending", tone: "muted", hint: "The change was abandoned before a decision." };
    default:
      return { label: "Not reported", tone: "muted", hint: "The durable record has no review state." };
  }
}

export function changeSpend(change) {
  const tokens = Number(change?.total_tokens) || 0;
  const cost = Number(change?.total_cost_usd) || 0;
  const reported = tokens > 0 || cost > 0;
  return {
    tokens,
    cost,
    reported,
    tokensLabel: tokens > 0 ? formatNumber(tokens) : "Not reported",
    tokensCompactLabel: tokens > 0 ? formatNumber(tokens, { compact: true }) : "Not reported",
    costLabel: reported ? formatUsd(cost) : "Not reported",
  };
}

// Aggregate spend for the loaded page. A page where every change reports zero
// tokens and zero cost is reported as unreported, not as "$0.00 of spend".
export function pageSpend(changes) {
  let tokens = 0;
  let cost = 0;
  let reported = false;
  for (const change of Array.isArray(changes) ? changes : []) {
    tokens += Number(change?.total_tokens) || 0;
    cost += Number(change?.total_cost_usd) || 0;
    if (Number(change?.total_tokens) > 0 || Number(change?.total_cost_usd) > 0) reported = true;
  }
  return {
    tokens,
    cost,
    reported,
    tokensLabel: tokens > 0 ? formatNumber(tokens) : "Not reported",
    tokensCompactLabel: tokens > 0 ? formatNumber(tokens, { compact: true }) : "Not reported",
    costLabel: reported ? formatUsd(cost) : "Not reported",
  };
}

function tokenValue(value) {
  // Number(null) is 0, so null/undefined must be rejected before coercion or
  // unreported token counts would masquerade as a reported zero.
  if (value === null || value === undefined || value === "") return null;
  const number = Number(value);
  return Number.isFinite(number) ? number : null;
}

export function runTokenTotal(run) {
  const input = tokenValue(run?.tokens_in);
  const output = tokenValue(run?.tokens_out);
  if (input === null && output === null) return null;
  return (input || 0) + (output || 0);
}

export function latestRun(runs) {
  const list = Array.isArray(runs) ? runs : [];
  return list.length ? list[0] : null;
}

export function activationReason(change, runID) {
  const activations = Array.isArray(change?.activations) ? change.activations : [];
  const match = activations.find((activation) => activation?.run_id === runID);
  return match?.reason || "";
}

// Activations recorded in the change lineage whose run evidence is no longer
// retained. The detail payload only returns resolvable runs, so surfacing the
// gap keeps the screen honest about pruned evidence.
export function missingActivationRuns(change, runs) {
  const known = new Set((Array.isArray(runs) ? runs : []).map((run) => run?.run_id).filter(Boolean));
  return (Array.isArray(change?.activations) ? change.activations : []).filter(
    (activation) => activation?.run_id && !known.has(activation.run_id),
  );
}

const DAY_MS = 24 * 60 * 60 * 1000;

export function waitingTone(waitingMS) {
  const value = Number(waitingMS);
  if (!Number.isFinite(value)) return "muted";
  if (value >= 7 * DAY_MS) return "danger";
  if (value >= DAY_MS) return "warning";
  return "info";
}

export function waitingHint(waitingMS) {
  const value = Number(waitingMS);
  if (!Number.isFinite(value)) return "Waiting time not reported.";
  if (value >= 7 * DAY_MS) return "Waiting longer than a week — escalate this decision.";
  if (value >= DAY_MS) return "Waiting longer than a day.";
  return "Recently queued.";
}

export function reviewKindMeta(kind) {
  if (kind === "paused_run") return { label: "Paused run", tone: "info" };
  return { label: "In review", tone: "warning" };
}

export function repositoryOf(item) {
  return item?.change?.repository || item?.run?.repository || "";
}

export function scopeOf(item) {
  return item?.change?.scope || item?.run?.scope || "";
}

export function revisionOf(item) {
  return item?.run?.requested_revision || item?.change?.requested_revision || "";
}

export function queueOldest(items) {
  const list = Array.isArray(items) ? items : [];
  let oldest = null;
  for (const item of list) {
    if (!Number.isFinite(Number(item?.waiting_ms))) continue;
    if (!oldest || Number(item.waiting_ms) > Number(oldest.waiting_ms)) oldest = item;
  }
  return oldest;
}

export function pluralize(count, singular, plural = `${singular}s`) {
  return `${formatNumber(count)} ${Number(count) === 1 ? singular : plural}`;
}
