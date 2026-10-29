import { useCallback, useEffect, useState } from "react";
import clsx from "clsx";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { formatNumber } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";

export const PAGE_SIZE_OPTIONS = [25, 50, 100].map((size) => ({ value: String(size), label: `${size} / page` }));

// useCursorPager stores the current offset cursor plus the cursors already
// visited, so "Previous" can walk back without server-side offset links.
// `filterKey` ties the pager to the active filter set: when it changes, the
// rendered cursor resets to page one on the same render, which avoids a wasted
// request with the stale cursor.
export function useCursorPager(filterKey) {
  const [state, setState] = useState(() => ({ key: filterKey, cursor: "", history: [] }));
  const current = state.key === filterKey ? state : { key: filterKey, cursor: "", history: [] };

  // Normalise the stored pager after the filter set changes. The derived
  // `current` above already renders page one, so this only prevents a stale
  // cursor from being restored when an operator returns to an older filter.
  useEffect(() => {
    setState((previous) => (previous.key === filterKey ? previous : { key: filterKey, cursor: "", history: [] }));
  }, [filterKey]);

  const goNext = useCallback(
    (nextCursor) => {
      if (!nextCursor) return;
      setState({ key: filterKey, cursor: nextCursor, history: [...current.history, current.cursor] });
    },
    [filterKey, current.cursor, current.history],
  );

  const goPrev = useCallback(() => {
    setState((previous) => {
      const base = previous.key === filterKey ? previous : { key: filterKey, cursor: "", history: [] };
      const history = [...base.history];
      const cursor = history.length ? history.pop() : "";
      return { key: filterKey, cursor, history };
    });
  }, [filterKey]);

  const reset = useCallback(() => setState({ key: filterKey, cursor: "", history: [] }), [filterKey]);

  return {
    cursor: current.cursor,
    pageIndex: current.history.length + 1,
    canPrev: current.history.length > 0,
    goNext,
    goPrev,
    reset,
  };
}

// Pagination is the shared cursor footer. Screens own the range copy because
// the review queue reports both page-level and queue-level counts.
export function Pagination({
  page,
  itemCount,
  noun = "items",
  pageIndex = 1,
  pageCount = 1,
  rangeText,
  pageSize,
  pageSizeOptions,
  onPageSizeChange,
  onPrev,
  onNext,
  prevDisabled,
  nextDisabled,
  loading = false,
  children,
  className,
}) {
  const offset = Number(page?.offset) || 0;
  const total = Number(page?.total) || 0;
  const count = Number(itemCount) || 0;

  let label = rangeText;
  if (!label) {
    if (!count) label = `0 ${noun}`;
    else label = `${formatNumber(offset + 1)}–${formatNumber(offset + count)} of ${formatNumber(total)} ${noun}`;
  }

  return (
    <div
      className={clsx(
        "flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-border px-3 py-2 text-[11px] text-muted-foreground",
        className,
      )}
    >
      <p className="tabular min-w-0">{label}</p>
      {children}
      <div className="ml-auto flex flex-wrap items-center gap-2">
        {pageSizeOptions ? (
          <Select
            ariaLabel="Rows per page"
            className="w-[104px]"
            value={String(pageSize)}
            onChange={(event) => onPageSizeChange?.(Number(event.target.value))}
            options={pageSizeOptions}
            disabled={loading}
          />
        ) : null}
        {pageCount > 1 ? (
          <span className="tabular hidden sm:inline">
            Page {formatNumber(pageIndex)} / {formatNumber(pageCount)}
          </span>
        ) : null}
        <Button
          variant="secondary"
          size="xs"
          onClick={onPrev}
          disabled={prevDisabled || loading}
          icon={<ChevronLeft />}
          aria-label="Previous page"
        >
          Prev
        </Button>
        <Button
          variant="secondary"
          size="xs"
          onClick={onNext}
          disabled={nextDisabled || loading}
          iconRight={<ChevronRight />}
          aria-label="Next page"
        >
          Next
        </Button>
      </div>
    </div>
  );
}
