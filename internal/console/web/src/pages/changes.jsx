import { useCallback, useMemo, useState } from "react";
import clsx from "clsx";
import { ChevronRight, ClipboardCheck, FileWarning, GitBranch, SlidersHorizontal, X } from "lucide-react";
import { useApi, useDebounced } from "@/lib/hooks";
import { useWorkspace } from "@/lib/workspace";
import {
  changeStatusLabel,
  changeStatusTone,
  formatDateTime,
  formatNumber,
  repoLabel,
  timeAgo,
  truncate,
  windowLabel,
} from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { PageHeader } from "@/components/ui/page-header";
import { SearchInput } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { StatusPill } from "@/components/ui/status-pill";
import { Table, THead, TH, TR, TD, TableEmpty } from "@/components/ui/table";
import { TableSkeleton } from "@/components/ui/skeleton";
import { FacetChips } from "@/components/flows/facet-chips";
import { Pagination, PAGE_SIZE_OPTIONS, useCursorPager } from "@/components/flows/pagination";
import { StatStrip, StatStripSkeleton } from "@/components/flows/stat-strip";
import { ErrorPanel } from "@/components/flows/error-panel";
import { Notice } from "@/components/flows/notice";
import { RefreshControl } from "@/components/flows/refresh-control";
import {
  CHANGE_STATUS_ORDER,
  changeSpend,
  facetMap,
  facetOptions,
  pageSpend,
  pluralize,
  reviewStateMeta,
} from "@/components/flows/helpers";

const SORT_OPTIONS = [
  { value: "-updated_at", label: "Recently updated" },
  { value: "+updated_at", label: "Least recently updated" },
  { value: "-created_at", label: "Newest created" },
  { value: "+created_at", label: "Oldest created" },
  { value: "-attempts", label: "Most attempts" },
  { value: "+attempts", label: "Fewest attempts" },
];

const COLUMNS = 9;

function SpendCell({ spend }) {
  if (!spend.reported) {
    return <span className="text-[11px] text-muted-foreground">Not reported</span>;
  }
  return (
    <div className="flex flex-col items-end gap-0.5">
      <span className="tabular text-[12px] leading-4 text-foreground">
        {spend.tokens > 0 ? `${formatNumber(spend.tokens)} tok` : "tokens not reported"}
      </span>
      <span className="tabular text-[10.5px] leading-4 text-muted-foreground">{spend.costLabel}</span>
    </div>
  );
}

export function ChangesPage() {
  const { window: windowKey, setWindow, autoRefresh, refreshNonce, refreshAll } = useWorkspace();
  const [query, setQuery] = useState("");
  const debouncedQuery = useDebounced(query, 300);
  const [statuses, setStatuses] = useState([]);
  const [repository, setRepository] = useState("");
  const [scope, setScope] = useState("");
  const [sort, setSort] = useState("-updated_at");
  const [limit, setLimit] = useState(25);

  // Pagination is keyed to the full filter set so any filter change returns to
  // page one without a request carrying the previous cursor.
  const filterKey = [debouncedQuery, statuses.join(","), repository, scope, sort, limit, windowKey].join("|");
  const pager = useCursorPager(filterKey);

  const { data, error, loading, updatedAt } = useApi("/changes", {
    params: {
      window: windowKey,
      query: debouncedQuery,
      status: statuses,
      repository,
      scope,
      sort,
      limit,
      cursor: pager.cursor,
    },
    pollMs: autoRefresh ? 30000 : 0,
    deps: [refreshNonce],
  });

  const handleRefresh = useCallback(() => refreshAll(), [refreshAll]);

  const changes = useMemo(() => (Array.isArray(data?.changes) ? data.changes : []), [data]);
  const page = data?.page || {};
  const facets = data?.facets || {};
  const pageCount = Math.max(1, Math.ceil((Number(page.total) || 0) / limit));
  const filtersActive = Boolean(debouncedQuery || statuses.length || repository || scope);

  const statusCounts = useMemo(() => facetMap(facets, "statuses"), [facets]);
  const statusOptions = useMemo(
    () =>
      CHANGE_STATUS_ORDER.map((value) => ({
        value,
        label: changeStatusLabel(value),
        count: statusCounts.get(value) || 0,
        tone: changeStatusTone(value),
      })),
    [statusCounts],
  );

  const repositoryOptions = useMemo(() => {
    return [
      { value: "", label: "All repositories" },
      ...facetOptions(facets, "repositories").map((facet) => ({
        value: facet.value,
        label: `${facet.value} (${formatNumber(facet.count)})`,
      })),
    ];
  }, [facets]);

  const scopeOptions = useMemo(() => {
    return [
      { value: "", label: "All scopes" },
      ...facetOptions(facets, "scopes").map((facet) => ({
        value: facet.value,
        label: `${facet.value} (${formatNumber(facet.count)})`,
      })),
    ];
  }, [facets]);

  const pageSpendInfo = useMemo(() => pageSpend(changes), [changes]);

  const stats = useMemo(() => {
    const total = Number(page.total) || 0;
    const inReview = statusCounts.get("IN_REVIEW") || 0;
    const open = statusCounts.get("OPEN") || 0;
    const done = statusCounts.get("DONE") || 0;
    const abandoned = statusCounts.get("ABANDONED") || 0;
    return [
      {
        key: "total",
        label: "Changes in view",
        value: formatNumber(total),
        sub: windowLabel(windowKey),
        hint: "Changes whose last update falls inside the selected window, after every active filter.",
      },
      {
        key: "in-review",
        label: "In review",
        value: formatNumber(inReview),
        tone: "warning",
        sub: "awaiting decision",
        hint: "Status facet count for the window and search, across all repositories and scopes.",
      },
      {
        key: "open",
        label: "In progress",
        value: formatNumber(open),
        tone: "info",
        sub: "status OPEN",
        hint: "Changes that are still open for execution or rework.",
      },
      {
        key: "done",
        label: "Approved",
        value: formatNumber(done),
        tone: "success",
        sub: "status DONE",
        hint: "Changes that reached the approved terminal state.",
      },
      {
        key: "abandoned",
        label: "Abandoned",
        value: formatNumber(abandoned),
        muted: abandoned === 0,
        sub: abandoned === 0 ? "none in window" : "closed without decision",
        hint: "Changes abandoned before approval.",
      },
      {
        key: "tokens",
        label: "Page tokens",
        value: pageSpendInfo.tokens > 0 ? pageSpendInfo.tokensCompactLabel : "Not reported",
        muted: pageSpendInfo.tokens === 0,
        sub: `${pluralize(changes.length, "change")} loaded`,
        hint: "Sum of total_tokens over the changes on this page. Zero means the artifacts did not report token usage, not that usage was zero.",
      },
      {
        key: "cost",
        label: "Page cost",
        value: pageSpendInfo.costLabel,
        muted: !pageSpendInfo.reported,
        sub: "reported cost only",
        hint: "Sum of total_cost_usd over the changes on this page. The console never estimates cost.",
      },
    ];
  }, [page.total, statusCounts, windowKey, pageSpendInfo, changes.length]);

  const toggleStatus = useCallback((value) => {
    setStatuses((current) => (current.includes(value) ? current.filter((entry) => entry !== value) : [...current, value]));
  }, []);

  const clearFilters = useCallback(() => {
    setQuery("");
    setStatuses([]);
    setRepository("");
    setScope("");
  }, []);

  const initialLoad = loading && !data;

  return (
    <div className="mx-auto w-full max-w-[1600px] px-4 py-5 sm:px-6 lg:px-8">
      <PageHeader
        eyebrow="Factory"
        title="Changes"
        description="Durable work items with their attempts, aggregate spend and review state. Open a change to inspect its activation runs and retained evidence."
        actions={
          <>
            <Button as="a" href="#/reviews" variant="outline" size="sm" icon={<ClipboardCheck />}>
              Review queue
            </Button>
            <RefreshControl updatedAt={data?.generated_at || updatedAt} loading={loading} onRefresh={handleRefresh} />
          </>
        }
        meta={
          <>
            <Badge variant="muted" size="sm" dot>
              Window: {windowLabel(windowKey)}
            </Badge>
            <span className="tabular">
              {data ? `${pluralize(Number(page.total) || 0, "change")} ${Number(page.total) === 1 ? "matches" : "match"}` : "Loading changes…"}
            </span>
            <span aria-hidden="true" className="text-border-strong">
              ·
            </span>
            <span>{autoRefresh ? "Auto-refresh every 30s" : "Auto-refresh paused"}</span>
          </>
        }
      />

      {error && !data ? (
        <ErrorPanel error={error} onRetry={handleRefresh} title="The change portfolio could not be loaded" />
      ) : (
        <div className="space-y-4">
          {initialLoad ? (
            <StatStripSkeleton
              items={7}
              columnsClassName="grid-cols-2 sm:grid-cols-3 md:grid-cols-4 xl:grid-cols-7"
            />
          ) : (
            <StatStrip
              items={stats}
              label="Change portfolio summary"
              columnsClassName="grid-cols-2 sm:grid-cols-3 md:grid-cols-4 xl:grid-cols-7"
            />
          )}

          <Card className="overflow-hidden">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-2 border-b border-border px-3 py-2">
              <SearchInput
                className="w-full min-w-0 sm:w-72"
                value={query}
                placeholder="Search changes by ID, task or repository…"
                onChange={(event) => setQuery(event.target.value)}
                onClear={() => setQuery("")}
              />
              <span aria-hidden="true" className="hidden h-4 w-px bg-border sm:block" />
              <FacetChips
                label="Filter by change status"
                options={statusOptions}
                selected={statuses}
                onToggle={toggleStatus}
              />
              <div className="ml-auto flex flex-wrap items-center gap-2">
                <Select
                  ariaLabel="Filter by repository"
                  value={repository}
                  onChange={(event) => setRepository(event.target.value)}
                  options={repositoryOptions}
                  className="w-[180px]"
                />
                <Select
                  ariaLabel="Filter by scope"
                  value={scope}
                  onChange={(event) => setScope(event.target.value)}
                  options={scopeOptions}
                  className="w-[132px]"
                />
                <Select
                  ariaLabel="Sort changes"
                  value={sort}
                  onChange={(event) => setSort(event.target.value)}
                  options={SORT_OPTIONS}
                  className="w-[170px]"
                />
              </div>
            </div>

            {filtersActive ? (
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-border bg-surface-raised/50 px-3 py-1.5 text-[11px] text-muted-foreground">
                <SlidersHorizontal aria-hidden="true" className="h-3 w-3 shrink-0" />
                <span className="tabular">{pluralize(Number(page.total) || 0, "change")} match these filters</span>
                {statuses.length ? <span className="truncate">· {statuses.map(changeStatusLabel).join(", ")}</span> : null}
                {repository ? (
                  <span className="min-w-0 truncate" title={repository}>
                    · {repoLabel(repository)}
                  </span>
                ) : null}
                {scope ? <span>· scope {scope}</span> : null}
                {debouncedQuery ? <span className="min-w-0 truncate">· “{debouncedQuery}”</span> : null}
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={clearFilters}
                  icon={<X />}
                  className="ml-auto text-muted-foreground hover:text-foreground"
                >
                  Clear filters
                </Button>
              </div>
            ) : null}

            {error && data ? (
              <div className="border-b border-border p-3">
                <Notice tone="warning" title="Refresh failed" icon={<FileWarning />}>
                  {error.message} Showing evidence generated {data?.generated_at ? timeAgo(data.generated_at) : "recently"}.
                </Notice>
              </div>
            ) : null}

            {initialLoad ? (
              <TableSkeleton rows={8} columns={8} showHeader={false} className="rounded-none border-0" />
            ) : (
              <div aria-busy={loading || undefined} className={clsx(loading && data && "opacity-60 transition-opacity")}>
                <Table minWidth={1180}>
                  <THead>
                    <TR>
                      <TH>Change</TH>
                      <TH>Status</TH>
                      <TH>Repository</TH>
                      <TH numeric>Attempts</TH>
                      <TH>Last result</TH>
                      <TH numeric>Spend</TH>
                      <TH>Updated</TH>
                      <TH>Review</TH>
                      <TH className="w-8">
                        <span className="sr-only">Open change</span>
                      </TH>
                    </TR>
                  </THead>
                  <tbody>
                    {changes.length === 0 ? (
                      <TableEmpty
                        colSpan={COLUMNS}
                        title={filtersActive ? "No changes match these filters" : "No changes recorded in this window"}
                        description={
                          filtersActive
                            ? "Broaden the search, clear the status, repository or scope filters, or widen the window in the top bar."
                            : "The artifact store has no changes whose last update falls inside the selected window."
                        }
                        action={
                          filtersActive ? (
                            <Button variant="secondary" size="sm" onClick={clearFilters} icon={<X />}>
                              Clear filters
                            </Button>
                          ) : (
                            <div className="flex flex-wrap items-center justify-center gap-2">
                              <Button variant="secondary" size="sm" onClick={() => setWindow("all")}>
                                Search all time
                              </Button>
                              <Button as="a" href="#/reviews" variant="ghost" size="sm" icon={<ClipboardCheck />}>
                                Open review queue
                              </Button>
                            </div>
                          )
                        }
                        icon={<GitBranch aria-hidden="true" />}
                      />
                    ) : (
                      changes.map((change) => {
                        const spend = changeSpend(change);
                        const review = reviewStateMeta(change);
                        const href = `#/changes/${encodeURIComponent(change.change_id)}`;
                        const preview = truncate(change.task_preview, 92);
                        return (
                          <TR key={change.change_id} className="relative">
                            <TD>
                              <div className="flex min-w-0 flex-col gap-0.5">
                                <div className="flex min-w-0 items-center gap-1.5">
                                  <a
                                    href={href}
                                    title={change.change_id}
                                    aria-label={`Open change ${change.change_id}`}
                                    className="mono min-w-0 truncate text-[12px] font-medium text-foreground underline-offset-4 after:absolute after:inset-0 after:z-0 after:content-[''] hover:text-primary hover:underline focus-visible:outline-offset-4"
                                  >
                                    {change.change_id}
                                  </a>
                                  {change.has_evidence ? null : (
                                    <span title="No retained run evidence for this change">
                                      <FileWarning aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-warning" />
                                    </span>
                                  )}
                                </div>
                                {preview ? (
                                  <p className="max-w-[280px] truncate text-[11px] leading-4 text-muted-foreground" title={change.task_preview}>
                                    {preview}
                                  </p>
                                ) : null}
                              </div>
                            </TD>
                            <TD>
                              <StatusPill kind="change" state={change.status} />
                            </TD>
                            <TD>
                              <div className="flex min-w-0 flex-col gap-0.5">
                                <span className="min-w-0 max-w-[240px] truncate text-[12px]" title={change.repository}>
                                  {repoLabel(change.repository)}
                                </span>
                                <span className="inline-flex w-fit items-center gap-1 rounded border border-border bg-surface-raised px-1.5 text-[10px] uppercase leading-4 tracking-[0.08em] text-muted-foreground">
                                  {change.scope || "no scope"}
                                </span>
                              </div>
                            </TD>
                            <TD numeric muted={!change.attempts} title="Recorded activation attempts">
                              {formatNumber(change.attempts)}
                            </TD>
                            <TD>
                              {change.last_result ? (
                                <StatusPill kind="run" state={change.last_result} />
                              ) : (
                                <span className="text-[11px] text-muted-foreground">No runs</span>
                              )}
                            </TD>
                            <TD numeric>
                              <SpendCell spend={spend} />
                            </TD>
                            <TD muted>
                              <span title={formatDateTime(change.updated_at)}>{timeAgo(change.updated_at)}</span>
                            </TD>
                            <TD>
                              <Badge variant={review.tone === "muted" ? "muted" : review.tone} size="sm" dot title={review.hint}>
                                {review.label}
                              </Badge>
                            </TD>
                            <TD>
                              <ChevronRight aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground/60" />
                            </TD>
                          </TR>
                        );
                      })
                    )}
                  </tbody>
                </Table>
              </div>
            )}

            <Pagination
              page={page}
              itemCount={changes.length}
              noun="changes"
              pageIndex={pager.pageIndex}
              pageCount={pageCount}
              pageSize={limit}
              pageSizeOptions={PAGE_SIZE_OPTIONS}
              onPageSizeChange={setLimit}
              onPrev={pager.goPrev}
              onNext={() => pager.goNext(page.next_cursor)}
              prevDisabled={!pager.canPrev}
              nextDisabled={!page.next_cursor}
              loading={loading}
            />
          </Card>

          <p className="px-1 text-[10.5px] leading-relaxed text-muted-foreground">
            Evidence source: <span className="mono">changes/*.json</span> in the durable artifact store. Status chips
            count every change in the selected window and search across all repositories and scopes; token and cost
            figures cover the changes loaded on this page and are never estimated.
          </p>
        </div>
      )}
    </div>
  );
}
