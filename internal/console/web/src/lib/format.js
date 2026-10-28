// Deterministic formatting helpers for the console.
//
// Every renderer that needs a number, duration or timestamp uses these so the
// overview, tables and detail pages cannot disagree about units.

export function formatNumber(value, { compact = false, fallback = "—" } = {}) {
  if (value === null || value === undefined || Number.isNaN(Number(value))) return fallback;
  const number = Number(value);
  if (compact) {
    return new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 }).format(number);
  }
  return new Intl.NumberFormat("en-US").format(number);
}

export function formatPercent(value, { digits = 1, fallback = "—" } = {}) {
  if (value === null || value === undefined || Number.isNaN(Number(value))) return fallback;
  const pct = Number(value) * 100;
  return `${pct.toFixed(pct >= 10 || pct === 0 ? 0 : digits)}%`;
}

export function formatDelta(value, { digits = 1, suffix = "pp" } = {}) {
  if (value === null || value === undefined || Number.isNaN(Number(value))) return "";
  const number = Number(value);
  const sign = number > 0 ? "+" : "";
  return `${sign}${number.toFixed(digits)}${suffix}`;
}

export function formatDuration(ms, { fallback = "—" } = {}) {
  if (ms === null || ms === undefined || Number.isNaN(Number(ms))) return fallback;
  const total = Math.max(0, Number(ms));
  if (total < 1000) return `${Math.round(total)}ms`;
  const seconds = total / 1000;
  if (seconds < 60) return `${seconds < 10 ? seconds.toFixed(1) : Math.round(seconds)}s`;
  const minutes = Math.floor(seconds / 60);
  const rest = Math.round(seconds % 60);
  if (minutes < 60) return rest ? `${minutes}m ${rest}s` : `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const remainingMinutes = minutes % 60;
  if (hours < 24) return remainingMinutes ? `${hours}h ${remainingMinutes}m` : `${hours}h`;
  const days = Math.floor(hours / 24);
  const remainingHours = hours % 24;
  return remainingHours ? `${days}d ${remainingHours}h` : `${days}d`;
}

export function formatBytes(value, { fallback = "—" } = {}) {
  if (value === null || value === undefined || Number.isNaN(Number(value))) return fallback;
  const bytes = Number(value);
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let size = bytes / 1024;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size < 10 ? size.toFixed(1) : Math.round(size)} ${units[unit]}`;
}

export function formatUsd(value, { fallback = "Not reported", digits = 2 } = {}) {
  if (value === null || value === undefined || Number.isNaN(Number(value))) return fallback;
  const number = Number(value);
  if (number === 0) return "$0.00";
  if (number < 0.01) return `$${number.toFixed(4)}`;
  return `$${number.toFixed(digits)}`;
}

export function formatDateTime(value, { fallback = "—" } = {}) {
  if (!value) return fallback;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return fallback;
  return new Intl.DateTimeFormat("en-US", {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function formatDate(value, { fallback = "—" } = {}) {
  if (!value) return fallback;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return fallback;
  return new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric" }).format(date);
}

export function timeAgo(value, now = Date.now()) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  const diff = Math.max(0, now - date.getTime());
  const seconds = Math.floor(diff / 1000);
  if (seconds < 45) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  if (months < 12) return `${months}mo ago`;
  return `${Math.floor(months / 12)}y ago`;
}

export function shortSha(value, length = 7) {
  if (!value) return "";
  return String(value).slice(0, length);
}

export function repoLabel(repository) {
  if (!repository) return "Unknown repository";
  const raw = String(repository);
  if (raw.startsWith("local:")) {
    const path = raw.slice("local:".length).replace(/\/+$/, "");
    const name = path.split("/").filter(Boolean).slice(-2).join("/");
    return name || "Local repository";
  }
  return raw
    .replace(/^https?:\/\//, "")
    .replace(/^ssh:\/\//, "")
    .replace(/^git@([^:]+):/, "$1/")
    .replace(/\.git$/, "");
}

export function truncate(value, length = 120) {
  if (!value) return "";
  const text = String(value);
  if (text.length <= length) return text;
  return `${text.slice(0, Math.max(0, length - 1)).trimEnd()}…`;
}

export function titleCase(value) {
  if (!value) return "";
  return String(value)
    .replace(/[_-]+/g, " ")
    .replace(/\b\w/g, (character) => character.toUpperCase());
}

const RUN_STATE_LABELS = {
  REQUESTED: "Requested",
  RUNNING: "Running",
  PAUSED: "Awaiting review",
  SUCCEEDED: "Succeeded",
  AGENT_FAILED: "Agent failed",
  VERIFICATION_FAILED: "Verification failed",
  CANCELLED: "Cancelled",
  INFRASTRUCTURE_FAILED: "Infrastructure failed",
  INVALID_REQUEST: "Invalid request",
  BLOCKED: "Blocked by agent",
  QUARANTINED: "Quarantined",
  GATE_TAMPERED: "Gate tampered",
  EGRESS_UNVERIFIED: "Egress unverified",
  REJECTED: "Rejected by reviewer",
};

export function runStateLabel(state) {
  return RUN_STATE_LABELS[state] || titleCase(state || "Unknown");
}

export function runStateTone(state) {
  switch (state) {
    case "SUCCEEDED":
      return "success";
    case "RUNNING":
    case "REQUESTED":
      return "info";
    case "PAUSED":
      return "warning";
    case "QUARANTINED":
    case "GATE_TAMPERED":
    case "EGRESS_UNVERIFIED":
      return "security";
    case "REJECTED":
    case "VERIFICATION_FAILED":
    case "AGENT_FAILED":
    case "INFRASTRUCTURE_FAILED":
    case "INVALID_REQUEST":
      return "danger";
    case "BLOCKED":
    case "CANCELLED":
      return "muted";
    default:
      return "muted";
  }
}

export function changeStatusLabel(status) {
  switch (status) {
    case "OPEN":
      return "In progress";
    case "IN_REVIEW":
      return "In review";
    case "DONE":
      return "Approved";
    case "ABANDONED":
      return "Abandoned";
    default:
      return titleCase(status || "Unknown");
  }
}

export function changeStatusTone(status) {
  switch (status) {
    case "DONE":
      return "success";
    case "IN_REVIEW":
      return "warning";
    case "OPEN":
      return "info";
    case "ABANDONED":
      return "muted";
    default:
      return "muted";
  }
}

export function cleanupTone(cleanup) {
  if (!cleanup || !cleanup.outcome) return "muted";
  if (cleanup.outcome === "SUCCESS" && cleanup.verified) return "success";
  if (cleanup.outcome === "SKIPPED") return "warning";
  return "danger";
}

export function severityTone(severity) {
  switch (String(severity || "").toLowerCase()) {
    case "critical":
    case "high":
      return "danger";
    case "medium":
      return "warning";
    case "low":
      return "info";
    default:
      return "muted";
  }
}

export function windowLabel(windowKey) {
  return { "24h": "Last 24 hours", "7d": "Last 7 days", "30d": "Last 30 days", "90d": "Last 90 days", all: "All time" }[windowKey] || "Last 30 days";
}

export function isInflight(state) {
  return state === "RUNNING" || state === "REQUESTED" || state === "PAUSED";
}

export function percentile(values, p) {
  const numbers = (values || []).filter((value) => typeof value === "number" && Number.isFinite(value)).sort((a, b) => a - b);
  if (!numbers.length) return null;
  const index = (numbers.length - 1) * p;
  const lower = Math.floor(index);
  const upper = Math.ceil(index);
  if (lower === upper) return numbers[lower];
  return numbers[lower] + (numbers[upper] - numbers[lower]) * (index - lower);
}

// parseUnifiedDiff turns a unified diff into per-file rows for the patch viewer.
export function parseUnifiedDiff(text) {
  const result = { files: [], additions: 0, deletions: 0, binary: false };
  if (!text) return result;
  let file = null;
  let inHunk = false;
  for (const line of String(text).split("\n")) {
    if (line.startsWith("diff --git ")) {
      const match = line.match(/^diff --git a\/(.+?) b\/(.+)$/);
      file = { path: match ? match[2] : line.slice("diff --git ".length), additions: 0, deletions: 0, lines: [] };
      result.files.push(file);
      inHunk = false;
      continue;
    }
    if (!file) continue;
    if (line.startsWith("Binary files ") || line.startsWith("GIT binary patch")) {
      result.binary = true;
      continue;
    }
    // File headers only occur outside a hunk. Inside a hunk, "+++ x" and
    // "--- x" are an added/removed line whose content itself starts with
    // "++ " or "-- ".
    if (!inHunk && (line.startsWith("+++ ") || line.startsWith("--- ") || line.startsWith("index "))) {
      continue;
    }
    if (line.startsWith("@@")) {
      inHunk = true;
      file.lines.push({ type: "hunk", text: line });
      continue;
    }
    if (line.startsWith("+")) {
      file.additions += 1;
      result.additions += 1;
      file.lines.push({ type: "add", text: line.slice(1) });
      continue;
    }
    if (line.startsWith("-")) {
      file.deletions += 1;
      result.deletions += 1;
      file.lines.push({ type: "del", text: line.slice(1) });
      continue;
    }
    file.lines.push({ type: "context", text: line.startsWith(" ") ? line.slice(1) : line });
  }
  return result;
}

export function patchSummaryFromText(text) {
  const parsed = parseUnifiedDiff(text);
  return {
    files: parsed.files.map((file) => ({ path: file.path, additions: file.additions, deletions: file.deletions })),
    additions: parsed.additions,
    deletions: parsed.deletions,
    binary: parsed.binary,
  };
}

export function initials(name) {
  if (!name) return "OP";
  return String(name)
    .split(/[\s@._-]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase() || "")
    .join("");
}
