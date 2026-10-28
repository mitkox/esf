import clsx from "clsx";
import { ArrowRight, BellRing, Inbox } from "lucide-react";
import { Card, CardFooter, CardHeader } from "@/components/ui/card";
import { Badge, StatusDot } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { TONE_TEXT } from "@/components/ui/tones";
import { formatDateTime, repoLabel, severityTone, timeAgo, titleCase } from "@/lib/format";

const SEVERITY_LABEL = { critical: "Critical", warning: "Warning", info: "Info" };

const KIND_LABEL = {
  failed_run: "Failed run",
  security_stop: "Security stop",
  cleanup_failure: "Cleanup failure",
  budget_exceeded: "Budget exceeded",
  review_pending: "Review pending",
  paused_run: "Paused run",
};

function severityMeta(severity) {
  const key = String(severity || "").toLowerCase();
  return {
    label: SEVERITY_LABEL[key] || titleCase(key || "Notice"),
    tone: TONE_TEXT[severityTone(key)] ? severityTone(key) : "muted",
    key,
  };
}

function kindLabel(kind) {
  return KIND_LABEL[kind] || titleCase(kind || "Attention");
}

function normalizeHref(href) {
  if (!href) return "#/overview";
  const value = String(href).trim();
  if (!value) return "#/overview";
  if (value.startsWith("#")) return value;
  return value.startsWith("/") ? `#${value}` : `#/${value}`;
}

function AttentionRow({ item, now }) {
  const severity = severityMeta(item.severity);
  const repository = item.repository ? repoLabel(item.repository) : "";
  const time = timeAgo(item.at, now);
  const detail = item.detail || "";
  const showRepository = repository && repository !== detail;

  return (
    <li>
      <a
        href={normalizeHref(item.href)}
        className="group flex gap-2.5 px-4 py-2.5 transition-colors hover:bg-surface-raised/70 sm:px-5"
      >
        <StatusDot tone={severity.tone} className="mt-1.5" />
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline justify-between gap-2">
            <p className="min-w-0 truncate text-[12px] font-semibold leading-5 text-foreground">{item.title}</p>
            <span
              className="shrink-0 tabular text-[10px] leading-4 text-muted-foreground"
              title={formatDateTime(item.at)}
            >
              {time}
            </span>
          </div>
          {detail ? (
            <p className="mt-0.5 line-clamp-2 break-words text-[11px] leading-4 text-muted-foreground" title={detail}>
              {detail}
            </p>
          ) : null}
          <div className="mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[10px] leading-4 text-muted-foreground/80">
            <span className={clsx("font-semibold uppercase tracking-[0.08em]", TONE_TEXT[severity.tone])}>
              {severity.label}
            </span>
            <span aria-hidden="true">·</span>
            <span>{kindLabel(item.kind)}</span>
            {showRepository ? (
              <>
                <span aria-hidden="true">·</span>
                <span className="min-w-0 max-w-[14rem] truncate" title={item.repository}>
                  {repository}
                </span>
              </>
            ) : null}
            {item.run_id ? (
              <>
                <span aria-hidden="true">·</span>
                <span className="mono max-w-[11rem] truncate" title={item.run_id}>
                  {item.run_id}
                </span>
              </>
            ) : null}
            {item.change_id && !item.run_id ? (
              <>
                <span aria-hidden="true">·</span>
                <span className="mono max-w-[11rem] truncate" title={item.change_id}>
                  {item.change_id}
                </span>
              </>
            ) : null}
            <ArrowRight
              aria-hidden="true"
              className="ml-auto h-3 w-3 shrink-0 text-muted-foreground/0 transition-colors group-hover:text-muted-foreground"
            />
          </div>
        </div>
      </a>
    </li>
  );
}

export function AttentionPanel({ items = [], now, className }) {
  const list = Array.isArray(items) ? items : [];
  const critical = list.filter((item) => String(item.severity).toLowerCase() === "critical").length;
  const tone = critical > 0 ? "danger" : list.length > 0 ? "warning" : "muted";

  return (
    <Card className={clsx("flex h-full min-w-0 flex-col", className)}>
      <CardHeader
        icon={<BellRing />}
        title="Needs attention"
        description={
          list.length
            ? critical > 0
              ? `${list.length} item${list.length === 1 ? "" : "s"} · ${critical} critical`
              : `${list.length} item${list.length === 1 ? "" : "s"} · newest first`
            : "Failures, security stops and decisions waiting on a human"
        }
        actions={
          <Button href="#/reviews" variant="ghost" size="xs">
            Review queue
          </Button>
        }
      />
      <div className="scroll-thin max-h-[360px] flex-1 overflow-y-auto">
        {list.length ? (
          <ul className="divide-y divide-border/70">
            {list.map((item, index) => (
              <AttentionRow key={item.id || `${item.kind}-${index}`} item={item} now={now} />
            ))}
          </ul>
        ) : (
          <EmptyState
            compact
            icon={<Inbox aria-hidden="true" className="h-4 w-4" />}
            title="Nothing needs attention"
            description="No failed runs, security stops, cleanup anomalies, budget overruns or pending reviews were returned for the current workspace."
          />
        )}
      </div>
      <CardFooter className="mt-auto">
        <span className="truncate">Ordered by severity, then most recent</span>
        {list.length ? (
          <Badge variant={tone} size="xs" dot className="ml-auto">
            {critical > 0 ? "Critical present" : "Advisory"}
          </Badge>
        ) : (
          <span className="ml-auto text-success">Queue clear</span>
        )}
      </CardFooter>
    </Card>
  );
}
