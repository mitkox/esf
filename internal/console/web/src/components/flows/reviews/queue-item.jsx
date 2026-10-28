import clsx from "clsx";
import { ExternalLink } from "lucide-react";
import { formatDateTime, formatDuration, repoLabel, shortSha, timeAgo } from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { StatusPill } from "@/components/ui/status-pill";
import { TONE_TEXT } from "@/components/ui/tones";
import { CommandRow } from "@/components/flows/command-row";
import {
  repositoryOf,
  revisionOf,
  reviewKindMeta,
  scopeOf,
  waitingHint,
  waitingTone,
} from "@/components/flows/helpers";

// ReviewQueueItem keeps waiting time as the lead metric: it is the first
// number the eye lands on and it carries the age tone (day / week thresholds).
export function ReviewQueueItem({ item, isOldest = false }) {
  const kind = reviewKindMeta(item?.kind);
  const run = item?.run || null;
  const change = item?.change || null;
  const live = item?.live || null;
  const repository = repositoryOf(item) || live?.repository || "";
  const scope = scopeOf(item) || live?.scope || "";
  const revision = revisionOf(item);
  const harness = run?.agent_harness || live?.harness || "";
  const previews = Array.isArray(item?.previews) ? item.previews : [];
  const changeHref = change?.change_id ? `#/changes/${encodeURIComponent(change.change_id)}` : "";
  const runHref = run?.run_id ? `#/runs/${encodeURIComponent(run.run_id)}` : live?.run_id ? `#/runs/${encodeURIComponent(live.run_id)}` : "";
  const tone = waitingTone(item?.waiting_ms);

  return (
    <Card as="li" className="overflow-hidden transition-colors hover:border-border-strong">
      <div className="flex flex-col gap-3 p-3.5 sm:p-4 xl:flex-row xl:items-start">
        <div className="flex shrink-0 items-start gap-3 xl:w-[172px] xl:flex-col xl:gap-1">
          <div className="flex min-w-0 flex-wrap items-baseline gap-2">
            <span
              className={clsx(
                "tabular text-[22px] font-semibold leading-none tracking-[-0.02em]",
                TONE_TEXT[tone] || "text-foreground",
              )}
              title={waitingHint(item?.waiting_ms)}
            >
              {formatDuration(item?.waiting_ms)}
            </span>
            {isOldest ? (
              <Badge variant="warning" size="xs" dot>
                Oldest
              </Badge>
            ) : null}
          </div>
          <div className="min-w-0 text-[10.5px] leading-4 text-muted-foreground">
            <p>waiting time</p>
            <p title={formatDateTime(item?.waiting_since)}>since {timeAgo(item?.waiting_since)}</p>
          </div>
        </div>

        <div className="min-w-0 flex-1 space-y-1.5">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <Badge variant={item?.kind === "paused_run" ? "info" : "warning"} size="sm" dot>
              {kind.label}
            </Badge>
            {run?.factory_result ? (
              <StatusPill kind="run" state={run.factory_result} size="xs" />
            ) : (
              <Badge variant="muted" size="sm" dot>
                Run evidence unavailable
              </Badge>
            )}
            {live ? (
              <Badge variant="info" size="sm" dot>
                Live orchestration
              </Badge>
            ) : null}
            {scope ? (
              <Badge variant="outline" size="xs" title="Factory scope">
                scope {scope}
              </Badge>
            ) : null}
            {harness ? <span className="text-[10.5px] text-muted-foreground">harness {harness}</span> : null}
            {run && Number.isFinite(Number(run.duration_ms)) ? (
              <span className="tabular text-[10.5px] text-muted-foreground">run {formatDuration(run.duration_ms)}</span>
            ) : null}
          </div>

          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            {changeHref ? (
              <a
                href={changeHref}
                className="mono min-w-0 break-all text-[12.5px] font-medium text-foreground underline-offset-4 hover:text-primary hover:underline"
                title={change?.change_id}
              >
                {change.change_id}
              </a>
            ) : (
              <span className="mono text-[12px] text-muted-foreground">No change record</span>
            )}
            {runHref ? (
              <a
                href={runHref}
                className="mono min-w-0 break-all text-[10.5px] text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
                title={run?.run_id || live?.run_id}
              >
                run {run?.run_id || live?.run_id}
              </a>
            ) : null}
          </div>

          <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground">
            <span className="inline-flex min-w-0 items-center gap-1">
              <span className="min-w-0 truncate" title={repository}>
                {repoLabel(repository)}
              </span>
            </span>
            {revision ? (
              <span className="mono" title={revision}>
                rev {shortSha(revision)}
              </span>
            ) : null}
          </p>

          {item?.detail ? <p className="break-words text-[11px] leading-relaxed text-muted-foreground">{item.detail}</p> : null}

          {live ? (
            <p className="break-words text-[11px] leading-relaxed text-muted-foreground">
              {live.current_step ? (
                <>
                  Current step <span className="text-foreground">{live.current_step}</span>.{" "}
                </>
              ) : null}
              {live.state ? (
                <>
                  State <span className="text-foreground">{live.state}</span>.{" "}
                </>
              ) : null}
              {live.sandbox_destroyed ? (
                "Sandbox already destroyed."
              ) : live.sandbox_id ? (
                <>
                  Sandbox <span className="mono text-[10.5px]">{live.sandbox_id}</span>.
                </>
              ) : null}
              {live.query_error ? <span className="text-warning"> {live.query_error}</span> : null}
            </p>
          ) : null}

          {previews.length ? (
            <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
              {previews.map((preview, index) => (
                <a
                  key={`${preview?.port || index}-${preview?.url || index}`}
                  href={preview?.url || "#"}
                  target="_blank"
                  rel="noreferrer noopener"
                  title="Run-provided preview link; it may not be reachable from this browser."
                  className="inline-flex items-center gap-1 rounded-md border border-border bg-surface-raised px-1.5 py-0.5 text-[10.5px] font-medium text-foreground transition-colors hover:border-border-strong"
                >
                  <ExternalLink aria-hidden="true" className="h-3 w-3" />
                  Preview{preview?.port ? ` :${preview.port}` : ""}
                </a>
              ))}
            </div>
          ) : null}
        </div>

        <div className="w-full shrink-0 space-y-1.5 border-t border-border pt-3 xl:w-[336px] xl:border-l xl:border-t-0 xl:pl-3 xl:pt-0 2xl:w-[380px]">
          {item?.actionable === false ? (
            <p className="rounded-lg border border-warning/30 bg-warning/5 px-2.5 py-2 text-[11px] leading-relaxed text-muted-foreground">
              <span className="font-semibold text-foreground">No live gate.</span>{" "}
              {item?.note || "This durable record has no paused workflow to answer."}
            </p>
          ) : (
            <>
              <CommandRow label="Approve" command={item?.approve_command} tone="success" />
              <CommandRow
                label="Reject"
                command={item?.reject_command}
                tone="danger"
                hint={item?.kind === "in_review" ? "Replace the … placeholder with a decision note." : undefined}
              />
            </>
          )}
        </div>
      </div>
    </Card>
  );
}
