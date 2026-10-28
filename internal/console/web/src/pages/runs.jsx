import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, ChevronLeft, ChevronRight, RefreshCw, SearchX } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardFooter } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Select } from "@/components/ui/select";
import { TableSkeleton } from "@/components/ui/skeleton";
import { TableEmpty } from "@/components/ui/table";
import { useApi, useDebounced } from "@/lib/hooks";
import { useWorkspace } from "@/lib/workspace";
import { formatNumber, runStateLabel, timeAgo, truncate, windowLabel } from "@/lib/format";
import { ActiveFilterChips, RunsToolbar } from "@/components/runs/runs-toolbar";
import { RunsStats } from "@/components/runs/runs-stats";
import { RunsTable } from "@/components/runs/run-table";
import {
  buildSelectOptions,
  DEFAULT_PAGE_SIZE,
  DEFAULT_SORT,
  PAGE_SIZES,
  repositorySelectOptions,
  stateFacetOptions,
} from "@/components/runs/utils";

function initialQuery(windowKey) {
  return {
    search: "",
    states: [],
    harness: "",
    repository: "",
    scope: "",
    sort: DEFAULT_SORT,
    limit: DEFAULT_PAGE_SIZE,
    offset: 0,
    windowKey,
  };
}

// RunsPage is the server-paginated evidence ledger. All filtering, sorting and
// pagination happen in the console API; this screen owns the query state, the
// debounced search field and the visible "why is this list empty" explanation.
export function RunsPage() {
  const { window: windowKey, autoRefresh, refreshNonce } = useWorkspace();
  const [search, setSearch] = useState("");
  const debouncedSearch = useDebounced(search, 300);
  const [query, setQuery] = useState(() => initialQuery(windowKey));

  // Debounced text lands in the server query only after the operator pauses.
  // Offset resets in the same update so a filter never fetches a stale page.
  useEffect(() => {
    setQuery((current) =>
      current.search === debouncedSearch ? current : { ...current, search: debouncedSearch, offset: 0 },
    );
  }, [debouncedSearch]);

  // A window change invalidates the current page offset. Deriving the effective
  // offset from the last synced window avoids a request at the old offset.
  useEffect(() => {
    setQuery((current) => (current.windowKey === windowKey ? current : { ...current, windowKey, offset: 0 }));
  }, [windowKey]);

  const offset = query.windowKey === windowKey ? query.offset : 0;

  const params = useMemo(
    () => ({
      window: windowKey,
      limit: query.limit,
      cursor: offset > 0 ? String(offset) : undefined,
      query: query.search || undefined,
      state: query.states.length > 0 ? query.states : undefined,
      harness: query.harness || undefined,
      repository: query.repository || undefined,
      scope: query.scope || undefined,
      sort: query.sort || DEFAULT_SORT,
    }),
    [windowKey, query.limit, offset, query.search, query.states, query.harness, query.repository, query.scope, query.sort],
  );

  const { data, error, loading, updatedAt, refresh } = useApi("/runs", {
    params,
    pollMs: autoRefresh ? 20000 : 0,
    deps: [refreshNonce],
  });

  const update = useCallback((partial) => {
    setQuery((current) => ({ ...current, ...partial, offset: 0 }));
  }, []);

  const toggleStates = useCallback((values) => update({ states: values }), [update]);
  const setHarness = useCallback((value) => update({ harness: value }), [update]);
  const setRepository = useCallback((value) => update({ repository: value }), [update]);
  const setScope = useCallback((value) => update({ scope: value }), [update]);
  const setSort = useCallback((value) => update({ sort: value }), [update]);
  const setLimit = useCallback(
    (value) => setQuery((current) => ({ ...current, limit: Number(value), offset: 0 })),
    [],
  );

  const clearFilters = useCallback(() => {
    setSearch("");
    setQuery((current) => ({
      ...current,
      search: "",
      states: [],
      harness: "",
      repository: "",
      scope: "",
      sort: DEFAULT_SORT,
      offset: 0,
    }));
  }, []);

  const activeFilters = useMemo(() => {
    const chips = [];
    if (query.search) {
      chips.push({
        key: "search",
        label: `Search “${truncate(query.search, 26)}”`,
        title: query.search,
        onRemove: () => {
          setSearch("");
          update({ search: "" });
        },
      });
    }
    for (const state of query.states) {
      chips.push({
        key: `state:${state}`,
        label: runStateLabel(state),
        onRemove: () => toggleStates(query.states.filter((value) => value !== state)),
      });
    }
    if (query.harness) {
      chips.push({ key: "harness", label: `Harness ${query.harness}`, onRemove: () => setHarness("") });
    }
    if (query.repository) {
      chips.push({ key: "repository", label: `Repository ${truncate(query.repository, 28)}`, title: query.repository, onRemove: () => setRepository("") });
    }
    if (query.scope) {
      chips.push({ key: "scope", label: `Scope ${query.scope}`, onRemove: () => setScope("") });
    }
    return chips;
  }, [query.search, query.states, query.harness, query.repository, query.scope, toggleStates, setHarness, setRepository, setScope, update]);

  const stateOptions = useMemo(() => stateFacetOptions(data?.facets?.states, query.states), [data?.facets?.states, query.states]);
  const harnessOptions = useMemo(
    () => buildSelectOptions(data?.facets?.harnesses, query.harness, "All harnesses"),
    [data?.facets?.harnesses, query.harness],
  );
  const repositoryOptions = useMemo(
    () => repositorySelectOptions(data?.facets?.repositories, query.repository),
    [data?.facets?.repositories, query.repository],
  );
  const scopeOptions = useMemo(
    () => buildSelectOptions(data?.facets?.scopes, query.scope, "All scopes"),
    [data?.facets?.scopes, query.scope],
  );

  const runs = data?.runs || [];
  const page = data?.page;
  const total = Number(page?.total) || 0;
  const pageSize = query.limit;
  // The server clamps an offset beyond the current total; display the clamped
  // position so the range label can never read "51–50 of 50".
  const displayOffset = Number.isFinite(Number(page?.offset)) ? Number(page.offset) : offset;
  const pageCount = Math.max(1, Math.ceil(total / Math.max(1, pageSize)));
  const currentPage = Math.floor(displayOffset / Math.max(1, pageSize)) + 1;
  const rangeStart = total === 0 ? 0 : displayOffset + 1;
  const rangeEnd = Math.min(displayOffset + pageSize, total);
  const refreshing = loading && Boolean(data);
  const index = data?.index;

  const header = (
    <PageHeader
      eyebrow="Factory ledger"
      title="Runs"
      description="Every factory activation across repositories, harnesses and scopes, with the durable evidence behind each result."
      meta={
        <>
          <span>{windowLabel(windowKey)}</span>
          <span aria-hidden="true" className="text-border-strong">
            ·
          </span>
          <span className="tabular">
            {data ? formatNumber(total) : "—"} run{total === 1 ? "" : "s"}
          </span>
          {updatedAt ? <span>Updated {timeAgo(updatedAt)}</span> : null}
          {refreshing ? <span className="text-muted-foreground">Refreshing…</span> : null}
          {index ? (
            <span>
              Index {formatNumber(index.runs)} runs / {formatNumber(index.changes)} changes
              {Number(index.scan_errors) > 0 ? ` · ${formatNumber(index.scan_errors)} scan errors` : ""}
            </span>
          ) : null}
        </>
      }
      actions={
        <Button variant="secondary" size="sm" icon={<RefreshCw />} loading={refreshing} onClick={refresh}>
          Refresh
        </Button>
      }
    />
  );

  if (!data && loading) {
    return (
      <div className="mx-auto w-full max-w-[1600px] px-4 py-5 sm:px-6 lg:px-8">
        {header}
        <RunsStats loading />
        <TableSkeleton rows={8} columns={9} />
      </div>
    );
  }

  if (!data && error) {
    return (
      <div className="mx-auto w-full max-w-[1600px] px-4 py-5 sm:px-6 lg:px-8">
        {header}
        <Card>
          <EmptyState
            icon={<AlertTriangle aria-hidden="true" />}
            title="Runs could not be loaded"
            description={error.message || "The console server did not respond."}
            action={
              <Button variant="primary" size="sm" icon={<RefreshCw />} onClick={refresh}>
                Try again
              </Button>
            }
          />
        </Card>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-[1600px] px-4 py-5 sm:px-6 lg:px-8">
      {header}

      {error ? (
        <div
          role="status"
          className="mb-4 flex flex-wrap items-center gap-2 rounded-panel border border-warning/30 bg-warning/5 px-3 py-2 text-[12px] text-warning"
        >
          <AlertTriangle aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
          <span className="min-w-0 flex-1">The latest refresh failed: {error.message}</span>
          <Button variant="ghost" size="xs" onClick={refresh} className="text-warning hover:text-warning">
            Retry
          </Button>
        </div>
      ) : null}

      <RunsStats data={data} windowKey={windowKey} searchActive={Boolean(query.search)} />

      <Card>
        <RunsToolbar
          search={search}
          onSearchChange={setSearch}
          query={query}
          total={total}
          stateOptions={stateOptions}
          harnessOptions={harnessOptions}
          repositoryOptions={repositoryOptions}
          scopeOptions={scopeOptions}
          onStatesChange={toggleStates}
          onHarnessChange={setHarness}
          onRepositoryChange={setRepository}
          onScopeChange={setScope}
          onSortChange={setSort}
          onClear={clearFilters}
          activeFilterCount={activeFilters.length}
        />
        <ActiveFilterChips chips={activeFilters} onClearAll={clearFilters} />

        <RunsTable
          runs={runs}
          empty={
            runs.length === 0 ? (
              <TableEmpty
                colSpan={10}
                icon={<SearchX aria-hidden="true" />}
                title="No runs match these filters"
                description={
                  activeFilters.length > 0
                    ? "Adjust or clear the active filters to widen the search."
                    : `No runs were recorded in ${windowLabel(windowKey).toLowerCase()}.`
                }
                action={
                  activeFilters.length > 0 ? (
                    <Button variant="secondary" size="sm" onClick={clearFilters}>
                      Clear filters
                    </Button>
                  ) : null
                }
              />
            ) : null
          }
        />

        <CardFooter className="justify-between gap-y-2">
          <span className="tabular text-[11px] text-muted-foreground">
            Showing {formatNumber(rangeStart)}–{formatNumber(rangeEnd)} of {formatNumber(total)}
          </span>
          <div className="ml-auto flex flex-wrap items-center gap-2">
            <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
              Rows
              <Select
                ariaLabel="Rows per page"
                value={String(pageSize)}
                onChange={(event) => setLimit(event.target.value)}
                options={PAGE_SIZES.map((size) => ({ value: String(size), label: String(size) }))}
                className="w-16"
              />
            </label>
            <span className="tabular px-1 text-[11px] text-muted-foreground">
              Page {currentPage} of {pageCount}
            </span>
            <Button
              variant="secondary"
              size="xs"
              icon={<ChevronLeft />}
              disabled={displayOffset === 0}
              aria-label="Previous page"
              onClick={() => setQuery((current) => ({ ...current, offset: Math.max(0, displayOffset - current.limit) }))}
            >
              Prev
            </Button>
            <Button
              variant="secondary"
              size="xs"
              iconRight={<ChevronRight />}
              disabled={!page?.next_cursor}
              aria-label="Next page"
              onClick={() => setQuery((current) => ({ ...current, offset: Number(page?.next_cursor) || 0 }))}
            >
              Next
            </Button>
          </div>
        </CardFooter>
      </Card>
    </div>
  );
}
