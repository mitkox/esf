import clsx from "clsx";
import {
  changeStatusLabel,
  changeStatusTone,
  cleanupTone,
  runStateLabel,
  runStateTone,
  severityTone,
  titleCase,
} from "@/lib/format";
import { StatusDot } from "./badge";
import { TONE_BORDER, TONE_SOFT, TONE_TEXT } from "./tones";

// StatusPill renders a domain state with an explicit text label plus a dot, so
// status is never communicated by colour alone. The raw enum is kept in the
// title attribute for auditors who need the exact machine value.

function runMeta(state) {
  return { label: runStateLabel(state), tone: runStateTone(state) };
}

function changeMeta(state) {
  return { label: changeStatusLabel(state), tone: changeStatusTone(state) };
}

function cleanupMeta(state) {
  const isObject = state !== null && typeof state === "object";
  const outcome = isObject ? state.outcome : state;
  const verified = isObject ? state.verified : undefined;
  if (!outcome) return { label: "Not reported", tone: "muted" };
  const tone = cleanupTone(isObject ? state : { outcome, verified: true });
  const value = String(outcome).toUpperCase();
  if (value === "SUCCESS") {
    if (verified === false) return { label: "Cleanup unverified", tone: "warning" };
    if (verified === true) return { label: "Cleanup verified", tone: "success" };
    return { label: "Cleanup succeeded", tone: tone === "muted" ? "success" : tone };
  }
  if (value === "SKIPPED") return { label: "Cleanup skipped", tone: "warning" };
  return { label: `Cleanup ${titleCase(outcome).toLowerCase()}`, tone: tone === "muted" ? "danger" : tone };
}

function reviewMeta(state) {
  const value = String(state ?? "").toUpperCase();
  switch (value) {
    case "APPROVED":
    case "PASSED":
    case "SUCCESS":
    case "SUCCEEDED":
      return { label: "Approved", tone: "success" };
    case "REJECTED":
    case "FAILED":
    case "DENIED":
      return { label: "Rejected", tone: "danger" };
    case "PENDING":
    case "AWAITING":
    case "AWAITING_REVIEW":
    case "REVIEW":
    case "IN_REVIEW":
    case "PAUSED":
      return { label: "Awaiting review", tone: "warning" };
    case "CANCELLED":
    case "SKIPPED":
    case "NONE":
    case "":
      return { label: "Not required", tone: "muted" };
    default:
      return { label: titleCase(state), tone: "muted" };
  }
}

function severityMeta(state) {
  const value = String(state ?? "").toLowerCase();
  if (!value || value === "none") return { label: value === "none" ? "No findings" : "Unknown", tone: "muted" };
  const tone = severityTone(state);
  return { label: titleCase(state), tone: tone === "muted" ? "info" : tone };
}

const META = {
  run: runMeta,
  change: changeMeta,
  cleanup: cleanupMeta,
  review: reviewMeta,
  severity: severityMeta,
};

const SIZES = {
  xs: "gap-1 px-1.5 py-0 text-[10px] leading-4",
  sm: "gap-1.5 px-2 py-0.5 text-[11px] leading-4",
};

export function StatusPill({ state, kind = "run", size = "sm", className }) {
  const meta = (META[kind] || runMeta)(state);
  const tone = TONE_TEXT[meta.tone] ? meta.tone : "muted";
  const raw = state !== null && typeof state === "object" ? state.outcome || "" : state;
  return (
    <span
      title={raw ? `State: ${raw}` : undefined}
      className={clsx(
        "inline-flex max-w-full items-center whitespace-nowrap rounded-full border font-medium",
        SIZES[size] || SIZES.sm,
        TONE_BORDER[tone],
        TONE_SOFT[tone],
        TONE_TEXT[tone],
        className,
      )}
    >
      <StatusDot tone={tone} size={size === "xs" ? "xs" : "sm"} />
      <span className="truncate">{meta.label}</span>
    </span>
  );
}
