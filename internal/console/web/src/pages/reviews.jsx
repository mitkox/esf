import { useCallback, useMemo, useState } from "react";
import { Clock, GitBranch, Inbox, Play, TriangleAlert, WifiOff } from "lucide-react";
import { useApi } from "@/lib/hooks";
import { useWorkspace } from "@/lib/workspace";
import { formatDateTime, formatDuration, formatNumber, timeAgo } from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Tabs } from "@/components/ui/tabs";
import { StatStrip, StatStripSkeleton } from "@/components/flows/stat-strip";
import { ErrorPanel } from "@/components/flows/error-panel";
import { Notice } from "@/components/flows/notice";
import { RefreshControl } from "@/components/flows/refresh-control";
import { Pagination, useCursorPager } from "@/components/flows/pagination";
import { ReviewQueueItem } from "@/components/flows/reviews/queue-item";
import { pluralize, queueOldest, waitingTone } from "@/components/flows/helpers";

const CONTAINER = "mx-auto w-full max-w-[1600px] px-4 py-5 sm:px-6 lg:px-8";
const STRIP_COLUMNS = "grid-cols-2 sm:grid-cols-3 xl:grid-cols-5";
const PAGE_SIZE = 25;

function QueueSkeleton({ rows = 3 }) {
  return (
    <div className="space-y-2 p-2.5 sm:p-3" role="status" aria-label="Loading review queue">
      {Array.from({ length: rows }).map((_, index) => (
        <div key={index} className="rounded-panel border border-border bg-surface p-4">
          <div className="flex flex-col gap-3 xl:flex-row">
            <div className="shrink-0 space-y-2 xl:w-[172px]">
              <div className="skeleton h-6 w-20" />
              <div className="skeleton h-2.5 w-24" />
            </div>
            <div className="min-w-0 flex-1 space-y-2">
              <div className="skeleton h-4 w-52 max-w-full" />
              <div className="skeleton h-3 w-72 max-w-full" />
              <div className="skeleton h-3 w-44 max-w-full" />
            </div>
            <div className="w-full shrink-0 space-y-2 xl:w-[336px]">
              <div className="skeleton h-12 w-full" />
              <div className="skeleton h-12 w-full" />
            </div>
          </div>
        </div>
      ))}
    </div>
  );
}

function itemKey(item, index) {
  return [item?.kind, item?.change?.change_id, item?.run?.run_id, item?.waiting_since].filter(Boolean).join(":") || `item-${index}`;
}

export function ReviewsPage() {
  const { autoRefresh, refreshNonce, refreshAll } = useWorkspace();
  const [kind, setKind] = useState("all");
  const pager = useCursorPager(kind);

  const { data, error, loading, updatedAt } = useApi("/reviews", {
    params: { limit: PAGE_SIZE, cursor: pager.cursor },
    pollMs: autoRefresh ? 15000 : 0,
    deps: [refreshNonce],
  });

  const handleRefresh = useCallback(() => refreshAll(), [refreshAll]);

  const items = useMemo(() => (Array.isArray(data?.items) ? data.items : []), [data]);
  const page = data?.page || {};
  const counts = useMemo(
    () => ({
      all: Number(data?.total) || 0,
      inReview: Number(data?.pending_review) || 0,
      paused: Number(data?.paused_runs) || 0,
    }),
    [data],
  );
  const liveAvailable = Boolean(data?.live_available);
  const unknownLiveRuns = Number(data?.unknown_live_runs) || 0;
  // Tab values are user-facing; API item kinds are `in_review` and
  // `paused_run`. Normalise so "Paused runs" does not silently empty out.
  const wantedKind = kind === "paused" ? "paused_run" : kind;
  const filtered = useMemo(
    () => (kind === "all" ? items : items.filter((item) => item?.kind === wantedKind)),
    [items, kind, wantedKind],
  );
  const oldest = useMemo(() => queueOldest(items), [items]);
  const pageIndex = pager.pageIndex;
  const pageCount = Math.max(1, Math.ceil(counts.all / PAGE_SIZE));

  const stats = useMemo(
    () => [
      {
        key: "queue",
        label: "Queue depth",
        value: formatNumber(counts.all),
        sub: `${pluralize(counts.all, "item")} awaiting a decision`,
        hint: "Every in-review change plus every paused run reported by the review index.",
      },
      {
        key: "pending",
        label: "Changes in review",
        value: formatNumber(counts.inReview),
        tone: "warning",
        sub: "status IN_REVIEW",
        hint: "Durable change records whose status is IN_REVIEW.",
      },
      {
        key: "paused",
        label: "Paused live runs",
        value: formatNumber(counts.paused),
        tone: counts.paused ? "info" : "muted",
        muted: counts.paused === 0,
        sub: liveAvailable ? "live orchestration" : "live feed unavailable",
        hint: "Runs paused in the live workflow engine. Zero while the live feed is unavailable means unknown, not verified clear.",
      },
      {
        key: "oldest",
        label: pageIndex > 1 ? "Oldest on this page" : "Oldest waiting",
        value: oldest ? formatDuration(oldest.waiting_ms) : "—",
        tone: oldest ? waitingTone(oldest.waiting_ms) : "muted",
        muted: !oldest,
        sub: oldest ? `since ${formatDateTime(oldest.waiting_since)}` : "no items loaded",
        subTitle: oldest?.waiting_since,
        hint: "Waiting time is measured from the recorded waiting_since timestamp and the review index clock.",
      },
      {
        key: "live",
        label: "Live feed",
        value: !data ? "—" : liveAvailable ? unknownLiveRuns ? "Partial" : "Available" : "Unavailable",
        tone: unknownLiveRuns ? "warning" : liveAvailable ? "success" : "muted",
        muted: !liveAvailable,
        sub: liveAvailable ? unknownLiveRuns ? "some run states unknown" : "paused runs included" : "durable records only",
        hint: "Whether the console could query live orchestration state. Unavailable means paused runs may be missing from this queue.",
      },
    ],
    [counts, liveAvailable, unknownLiveRuns, pageIndex, oldest, data],
  );

  const tabOptions = useMemo(
    () => [
      { id: "all", label: "All", count: counts.all },
      { id: "in_review", label: "In review", count: counts.inReview },
      { id: "paused", label: "Paused runs", count: counts.paused },
    ],
    [counts],
  );

  const rangeText = useMemo(() => {
    if (!data) return "";
    const offset = Number(page.offset) || 0;
    if (kind === "all") {
      if (!items.length) return "0 review items";
      return `${formatNumber(offset + 1)}–${formatNumber(offset + items.length)} of ${formatNumber(counts.all)} review items`;
    }
    const queueCount = kind === "in_review" ? counts.inReview : counts.paused;
    const label = kind === "in_review" ? "in review" : "paused";
    return `${formatNumber(filtered.length)} of ${formatNumber(items.length)} on this page · ${formatNumber(queueCount)} ${label} in queue`;
  }, [data, page.offset, kind, items.length, counts, filtered.length]);

  const filterCount = kind === "in_review" ? counts.inReview : counts.paused;
  const filterTitle = kind === "paused" ? "No paused runs on this page" : "No in-review changes on this page";
  const filterLabel = kind === "paused" ? "paused run" : "in-review change";
  const hasNextPage = Boolean(page.next_cursor);

  const initialLoad = loading && !data;

  return (
    <div className={CONTAINER}>
      <PageHeader
        eyebrow="Factory"
        title="Reviews"
        description="Human decisions waiting in the factory. Waiting time is the lead metric; every decision is executed in the authenticated CLI and copied from here, never run by the console."
        actions={
          <>
            <Button as="a" href="#/changes" variant="outline" size="sm" icon={<GitBranch />}>
              Changes
            </Button>
            <RefreshControl updatedAt={data?.generated_at || updatedAt} loading={loading} onRefresh={handleRefresh} />
          </>
        }
        meta={
          <>
            <Badge variant="muted" size="sm" dot>
              Oldest decision first
            </Badge>
            <span className="tabular">
              {data ? `${pluralize(counts.all, "item")} in queue` : "Loading queue…"}
            </span>
            {data ? (
              <>
                <span aria-hidden="true" className="text-border-strong">
                  ·
                </span>
                <span className="tabular">{formatNumber(counts.inReview)} in review</span>
                <span aria-hidden="true" className="text-border-strong">
                  ·
                </span>
                <span className="tabular">{formatNumber(counts.paused)} paused</span>
              </>
            ) : null}
            <span aria-hidden="true" className="text-border-strong">
              ·
            </span>
            <span>{autoRefresh ? "Auto-refresh every 15s" : "Auto-refresh paused"}</span>
            {data?.generated_at ? (
              <>
                <span aria-hidden="true" className="text-border-strong">
                  ·
                </span>
                <span title={formatDateTime(data.generated_at)}>Generated {timeAgo(data.generated_at)}</span>
              </>
            ) : null}
          </>
        }
      />

      {error && !data ? (
        <ErrorPanel error={error} onRetry={handleRefresh} title="The review queue could not be loaded" />
      ) : (
        <div className="space-y-4">
          {initialLoad ? (
            <StatStripSkeleton items={5} columnsClassName={STRIP_COLUMNS} />
          ) : (
            <StatStrip items={stats} label="Review queue summary" columnsClassName={STRIP_COLUMNS} />
          )}

          {data && !liveAvailable ? (
            <Notice tone="warning" title="Live orchestration state unavailable" icon={<WifiOff />}>
              Paused live runs cannot be listed right now, so this queue shows durable change review records only. Treat
              the paused-run count as unknown rather than zero.
              {data.live_error ? (
                <span className="mono mt-1 block break-words text-[10.5px] text-muted-foreground">{data.live_error}</span>
              ) : null}
            </Notice>
          ) : null}

          {unknownLiveRuns > 0 ? (
            <Notice tone="warning" title="Live review coverage is incomplete" icon={<TriangleAlert />}>
              Status is unknown for {pluralize(unknownLiveRuns, "live run")}. Additional review gates may be waiting;
              the paused-run count includes confirmed gates only. Check the factory CLI before treating this queue as clear.
            </Notice>
          ) : null}

          {error && data ? (
            <Notice tone="warning" title="Refresh failed" icon={<TriangleAlert />}>
              {error.message} Showing the last queue snapshot
              {data.generated_at ? ` generated ${timeAgo(data.generated_at)}` : ""}.
            </Notice>
          ) : null}

          <Card className="overflow-hidden">
            <div className="flex flex-wrap items-end gap-x-2 gap-y-1 border-b border-border px-3 pt-1.5">
              <Tabs tabs={tabOptions} value={kind} onChange={setKind} className="min-w-0" />
              <div className="ml-auto flex shrink-0 items-center gap-1.5 pb-2 text-[11px] text-muted-foreground">
                <Clock aria-hidden="true" className="h-3 w-3" />
                <span>Oldest first</span>
              </div>
            </div>

            {kind !== "all" ? (
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-border bg-surface-raised/40 px-3 py-1.5 text-[11px] text-muted-foreground">
                <span>
                  Kind filter applies to the loaded page. Queue totals come from the review index and reset pagination to
                  the first page.
                </span>
                <Button variant="ghost" size="xs" onClick={() => setKind("all")} className="ml-auto">
                  Show all
                </Button>
              </div>
            ) : null}

            {initialLoad ? (
              <QueueSkeleton />
            ) : error && !data ? (
              <div className="p-3">
                <ErrorPanel error={error} onRetry={handleRefresh} title="The review queue could not be loaded" />
              </div>
            ) : !data ? null : counts.all === 0 ? (
              <EmptyState
                icon={<Inbox aria-hidden="true" />}
                title={liveAvailable && !unknownLiveRuns ? "The review queue is clear" : "No confirmed review items"}
                description={
                  liveAvailable && !unknownLiveRuns
                    ? "No change is awaiting a human decision and no live run is paused for review. New items appear here as soon as a run reaches its review gate."
                    : "No durable change is awaiting a human decision. Some live run states could not be checked, so additional review gates may be waiting."
                }
                action={
                  <div className="flex flex-wrap items-center justify-center gap-2">
                    <Button as="a" href="#/changes" variant="secondary" size="sm" icon={<GitBranch />}>
                      Browse changes
                    </Button>
                    <Button as="a" href="#/runs" variant="ghost" size="sm" icon={<Play />}>
                      View runs
                    </Button>
                  </div>
                }
              />
            ) : items.length === 0 ? (
              <EmptyState
                compact
                icon={<Clock aria-hidden="true" />}
                title="This page is empty"
                description="The queue changed while you were paging. Return to the first page to reload the oldest items."
                action={
                  <Button variant="secondary" size="sm" onClick={pager.reset}>
                    Back to first page
                  </Button>
                }
              />
            ) : filtered.length === 0 ? (
              <EmptyState
                compact
                icon={<Clock aria-hidden="true" />}
                title={filterTitle}
                description={
                  filterCount > 0
                    ? `The queue reports ${pluralize(filterCount, filterLabel)}. ${
                        hasNextPage
                          ? "Continue to the next page to find them."
                          : "None of them fall on this page; return to the first page."
                      }`
                    : `No ${filterLabel} is reported by the queue right now.`
                }
                action={
                  <div className="flex flex-wrap items-center justify-center gap-2">
                    {hasNextPage ? (
                      <Button variant="secondary" size="sm" onClick={() => pager.goNext(page.next_cursor)}>
                        Next page
                      </Button>
                    ) : (
                      <Button variant="secondary" size="sm" onClick={pager.reset}>
                        Back to first page
                      </Button>
                    )}
                    <Button variant="ghost" size="sm" onClick={() => setKind("all")}>
                      Show all
                    </Button>
                  </div>
                }
              />
            ) : (
              <ol className="space-y-2 p-2.5 sm:p-3" aria-label="Review queue, oldest first">
                {filtered.map((item, index) => (
                  <ReviewQueueItem
                    key={itemKey(item, index)}
                    item={item}
                    isOldest={kind === "all" && pageIndex === 1 && index === 0}
                  />
                ))}
              </ol>
            )}

            <Pagination
              page={page}
              itemCount={items.length}
              noun="review items"
              rangeText={rangeText}
              pageIndex={pageIndex}
              pageCount={pageCount}
              onPrev={pager.goPrev}
              onNext={() => pager.goNext(page.next_cursor)}
              prevDisabled={!pager.canPrev}
              nextDisabled={!page.next_cursor}
              loading={loading}
            />
          </Card>

          <p className="px-1 text-[10.5px] leading-relaxed text-muted-foreground">
            Evidence source: durable change records with status <span className="mono">IN_REVIEW</span> plus paused runs
            queried from live orchestration. Waiting time is measured from the recorded{" "}
            <span className="mono">waiting_since</span> timestamp; the console does not track SLAs or mutate state.
          </p>
        </div>
      )}
    </div>
  );
}
