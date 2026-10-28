import clsx from "clsx";
import { Filter } from "lucide-react";
import { SearchInput } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Button } from "@/components/ui/button";
import { TableToolbar } from "@/components/ui/table";
import { formatNumber } from "@/lib/format";
import { FacetMultiSelect } from "./facet-multi-select";
import { SORT_OPTIONS } from "./utils";

// RunsToolbar owns only the controls; filter state and debouncing live in the
// page so a single source of truth drives the request and the visible chips.
export function RunsToolbar({
  search,
  onSearchChange,
  query,
  total,
  stateOptions,
  harnessOptions,
  repositoryOptions,
  scopeOptions,
  onStatesChange,
  onHarnessChange,
  onRepositoryChange,
  onScopeChange,
  onSortChange,
  onClear,
  activeFilterCount,
}) {
  return (
    <TableToolbar
      actions={
        <span className="tabular text-[11px] text-muted-foreground" aria-live="polite">
          {formatNumber(total)} run{total === 1 ? "" : "s"}
        </span>
      }
    >
      <SearchInput
        value={search}
        onChange={(event) => onSearchChange(event.target.value)}
        placeholder="Search run, repository, SHA…"
        aria-label="Search runs"
        className="w-full sm:w-64"
      />
      <FacetMultiSelect
        label="State"
        icon={Filter}
        options={stateOptions}
        values={query.states}
        onChange={onStatesChange}
        toneFor={(value) => stateOptions.find((option) => option.value === value)?.tone || "muted"}
      />
      <Select
        ariaLabel="Filter by harness"
        value={query.harness}
        onChange={(event) => onHarnessChange(event.target.value)}
        options={harnessOptions}
        className="w-40"
      />
      <Select
        ariaLabel="Filter by repository"
        value={query.repository}
        onChange={(event) => onRepositoryChange(event.target.value)}
        options={repositoryOptions}
        className="w-52"
      />
      <Select
        ariaLabel="Filter by scope"
        value={query.scope}
        onChange={(event) => onScopeChange(event.target.value)}
        options={scopeOptions}
        className="w-36"
      />
      <Select
        ariaLabel="Sort runs"
        value={query.sort}
        onChange={(event) => onSortChange(event.target.value)}
        options={SORT_OPTIONS}
        className="w-44"
      />
      <Button
        variant="ghost"
        size="sm"
        onClick={onClear}
        disabled={activeFilterCount === 0}
        className="text-muted-foreground"
      >
        Clear filters
      </Button>
    </TableToolbar>
  );
}

// Active filter chips give the operator a visible, removable summary of the
// current server-side filter. Removable chips stay keyboard reachable.
export function ActiveFilterChips({ chips = [], onClearAll, className }) {
  if (!chips.length) return null;
  return (
    <div
      className={clsx(
        "flex flex-wrap items-center gap-1.5 border-b border-border bg-surface px-3 py-2",
        className,
      )}
      aria-label="Active run filters"
    >
      <span className="mr-1 text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">Filters</span>
      {chips.map((chip) => (
        <span
          key={chip.key}
          className="inline-flex max-w-full items-center gap-1 rounded-md border border-border bg-muted px-2 py-0.5 text-[11px] text-foreground"
        >
          <span className="truncate" title={chip.title || chip.label}>
            {chip.label}
          </span>
          <button
            type="button"
            onClick={chip.onRemove}
            aria-label={`Remove filter ${chip.label}`}
            className="grid h-4 w-4 shrink-0 place-items-center rounded text-muted-foreground transition-colors hover:bg-surface-overlay hover:text-foreground"
          >
            <svg viewBox="0 0 12 12" aria-hidden="true" className="h-2.5 w-2.5">
              <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
            </svg>
          </button>
        </span>
      ))}
      {onClearAll ? (
        <button type="button" onClick={onClearAll} className="ml-1 text-[11px] font-medium text-primary hover:underline">
          Clear all
        </button>
      ) : null}
    </div>
  );
}
