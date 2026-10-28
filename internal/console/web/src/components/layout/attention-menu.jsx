import clsx from "clsx";
import { Bell, GitBranch, Inbox, Play, ShieldAlert } from "lucide-react";
import { hrefFor } from "@/routes";
import { formatDuration, repoLabel, severityTone, timeAgo, titleCase } from "@/lib/format";
import { StatusDot } from "@/components/ui/badge";
import { normalizeHref } from "./links";
import { useDismiss } from "./use-dismiss";

function attentionHref(item) {
  const explicit = item.href || item.link || item.url;
  if (explicit) return normalizeHref(explicit);
  const runID = item.run_id || item.runID || item.run;
  if (runID) return hrefFor("run", runID);
  const changeID = item.change_id || item.changeID || item.change;
  if (changeID) return hrefFor("change", changeID);
  return "#/reviews";
}

function attentionTone(item, kind) {
  const severity = item.severity || item.level || item.max_severity;
  if (severity) {
    const tone = severityTone(severity);
    if (tone !== "muted") return tone;
  }
  if (kind.includes("security") || kind.includes("hardening") || kind.includes("egress")) return "security";
  if (kind.includes("review") || kind.includes("paused") || kind.includes("awaiting")) return "warning";
  if (kind.includes("cleanup") || kind.includes("sandbox")) return "warning";
  return "danger";
}

function defaultTitle(kind) {
  if (kind.includes("review") || kind.includes("paused")) return "Run awaiting review";
  if (kind.includes("security")) return "Security finding";
  if (kind.includes("cleanup") || kind.includes("sandbox")) return "Sandbox cleanup needs attention";
  return "Needs attention";
}

function normalizeAttention(item, index) {
  if (!item || typeof item !== "object") return null;
  const kind = String(item.kind || item.type || item.category || "").toLowerCase();
  const title = item.title || item.message || item.summary || item.label || defaultTitle(kind);
  const runID = item.run_id || item.runID || "";
  const changeID = item.change_id || item.changeID || "";
  const repository = item.repository ? repoLabel(item.repository) : "";
  const meta = item.description || item.detail || [repository, runID || changeID].filter(Boolean).join(" · ");
  const at = item.at || item.created_at || item.timestamp || item.since || "";
  const Icon = kind.includes("security")
    ? ShieldAlert
    : kind.includes("change")
      ? GitBranch
      : kind.includes("run")
        ? Play
        : Bell;
  return {
    key: `${kind || "attention"}:${runID || changeID || index}`,
    title,
    meta,
    href: attentionHref(item),
    tone: attentionTone(item, kind),
    severity: item.severity || item.max_severity || "",
    at,
    Icon,
  };
}

function AttentionSkeleton() {
  return (
    <div role="status" aria-label="Loading attention queue" className="divide-y divide-border">
      {[0, 1, 2].map((row) => (
        <div key={row} className="flex items-start gap-2.5 px-3.5 py-3">
          <div className="skeleton mt-1 h-1.5 w-1.5 rounded-full" />
          <div className="min-w-0 flex-1 space-y-1.5">
            <div className="skeleton h-2.5 w-3/4" />
            <div className="skeleton h-2 w-1/2" />
          </div>
        </div>
      ))}
    </div>
  );
}

export function AttentionMenu({
  open,
  onOpenChange,
  data,
  loading,
  error,
  onRetry,
  health,
  attentionCount,
  reviews,
}) {
  const { panelRef, triggerRef } = useDismiss(open, () => onOpenChange(false));
  const raw = Array.isArray(data?.attention) ? data.attention : [];
  const reviewsPending = Number.isFinite(Number(reviews?.value)) ? Number(reviews.value) : 0;
  const reviewsPaused = Number.isFinite(Number(reviews?.paused_live)) ? Number(reviews.paused_live) : 0;
  const reviewsOldest = Number.isFinite(Number(reviews?.oldest_wait_ms)) ? Number(reviews.oldest_wait_ms) : 0;
  const items = raw.map(normalizeAttention).filter(Boolean);
  const count = Math.min(Number(attentionCount) || 0, 99);
  const healthTone = health?.tone || "muted";
  const empty = !loading && items.length === 0;

  return (
    <div className="relative">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => onOpenChange(!open)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Notifications${count > 0 ? `: ${count} needing attention` : ""}`}
        className="relative grid h-8 w-8 place-items-center rounded-lg border border-transparent text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      >
        <Bell aria-hidden="true" className="h-4 w-4" />
        {count > 0 ? (
          <span className="tabular absolute -right-0.5 -top-0.5 grid h-4 min-w-4 place-items-center rounded-full border border-background bg-danger px-1 text-[9px] font-bold leading-none text-background">
            {count}
          </span>
        ) : (
          <span
            aria-hidden="true"
            className={clsx(
              "absolute right-1.5 top-1.5 h-1.5 w-1.5 rounded-full",
              healthTone === "success" && "bg-success",
              healthTone === "warning" && "bg-warning",
              healthTone === "danger" && "bg-danger",
              healthTone === "muted" && "bg-muted-foreground/40",
            )}
          />
        )}
      </button>

      {open ? (
        <div
          ref={panelRef}
          role="menu"
          aria-label="Attention and notifications"
          className="dialog-panel fixed inset-x-3 top-[calc(var(--topbar-height)+0.5rem)] z-[60] overflow-hidden rounded-xl border border-border bg-surface-overlay shadow-popover sm:absolute sm:inset-x-auto sm:right-0 sm:top-[calc(100%+0.5rem)] sm:w-[360px]"
        >
          <div className="flex items-center gap-2 border-b border-border px-3.5 py-2.5">
            <StatusDot tone={healthTone} pulse={healthTone === "success"} />
            <span className="min-w-0 truncate text-xs font-semibold text-foreground">{health?.label || "Status unavailable"}</span>
            <span className="ml-auto shrink-0 text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
              Attention
            </span>
          </div>

          {error ? (
            <div className="px-3.5 py-5 text-center">
              <Inbox aria-hidden="true" className="mx-auto h-5 w-5 text-muted-foreground/60" />
              <p className="mt-2 text-xs font-medium text-foreground">Attention feed unavailable</p>
              <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">
                The overview endpoint did not answer. The rest of the console keeps working.
              </p>
              <button
                type="button"
                onClick={onRetry}
                className="mt-2.5 rounded-md border border-border bg-surface px-2.5 py-1 text-[11px] font-medium text-foreground transition-colors hover:bg-muted"
              >
                Retry
              </button>
            </div>
          ) : loading && items.length === 0 ? (
            <AttentionSkeleton />
          ) : empty ? (
            <div className="px-3.5 py-6 text-center">
              <Inbox aria-hidden="true" className="mx-auto h-5 w-5 text-muted-foreground/50" />
              <p className="mt-2 text-xs font-medium text-foreground">Nothing needs attention</p>
              <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">
                Paused runs, security stops and review requests appear here.
              </p>
            </div>
          ) : (
            <div className="scroll-thin max-h-[min(60vh,22rem)] divide-y divide-border overflow-y-auto">
              {items.slice(0, 12).map((item) => (
                <a
                  key={item.key}
                  role="menuitem"
                  href={item.href}
                  onClick={() => onOpenChange(false)}
                  className="flex items-start gap-2.5 px-3.5 py-2.5 transition-colors hover:bg-muted/60 focus-visible:bg-muted/60"
                >
                  <StatusDot tone={item.tone} className="mt-1.5" />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-xs font-medium leading-4 text-foreground">{item.title}</p>
                    {item.meta ? (
                      <p className="mt-0.5 line-clamp-2 text-[11px] leading-4 text-muted-foreground">{item.meta}</p>
                    ) : null}
                  </div>
                  <div className="flex shrink-0 flex-col items-end gap-1">
                    {item.severity ? (
                      <span className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                        {titleCase(item.severity)}
                      </span>
                    ) : null}
                    {item.at ? <span className="tabular text-[10px] leading-4 text-muted-foreground">{timeAgo(item.at)}</span> : null}
                  </div>
                </a>
              ))}
            </div>
          )}

          <div className="flex items-center justify-between gap-2 border-t border-border bg-surface/60 px-3.5 py-2">
            <a
              href="#/reviews"
              title={
                reviewsPaused > 0 ? `${reviewsPaused} paused run${reviewsPaused === 1 ? "" : "s"} live` : undefined
              }
              className="text-[11px] font-medium text-primary hover:underline"
            >
              Reviews
              {reviewsPending > 0 ? ` · ${reviewsPending} pending` : ""}
              {reviewsOldest > 0 ? ` · oldest ${formatDuration(reviewsOldest)}` : ""}
            </a>
            <a href="#/overview" className="text-[11px] text-muted-foreground hover:text-foreground">
              Open overview
            </a>
          </div>
        </div>
      ) : null}
    </div>
  );
}
