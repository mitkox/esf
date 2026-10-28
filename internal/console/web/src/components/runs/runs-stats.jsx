import clsx from "clsx";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { formatNumber, formatPercent, timeAgo, windowLabel } from "@/lib/format";
import { sumFacetCounts } from "./utils";

// RunsStats is the compact strip above the ledger. It reports the facet totals
// the server returned for the current window (before facet filters are applied)
// plus the evidence index freshness, so an operator can tell "no results"
// apart from "the index is empty".
export function RunsStats({ data, loading = false, windowKey, searchActive = false }) {
  if (loading && !data) {
    return (
      <Card className="mb-4 overflow-hidden">
        <div className="grid grid-cols-2 gap-px bg-border sm:grid-cols-3 xl:grid-cols-5">
          {[0, 1, 2, 3, 4].map((item) => (
            <div key={item} className="bg-surface p-3.5">
              <Skeleton className="h-2.5 w-20" />
              <Skeleton className="mt-2 h-5 w-16" />
              <Skeleton className="mt-2 h-2.5 w-24" />
            </div>
          ))}
        </div>
      </Card>
    );
  }

  const facets = data?.facets || {};
  const states = facets.states || [];
  const stateTotal = sumFacetCounts(states);
  const succeeded = states.find((facet) => facet.value === "SUCCEEDED")?.count || 0;
  const notSucceeded = Math.max(0, stateTotal - succeeded);
  const successRate = stateTotal > 0 ? succeeded / stateTotal : null;
  const index = data?.index;
  const scanErrors = Number(index?.scan_errors) || 0;

  const cells = [
    {
      key: "window",
      label: "Runs in view",
      value: formatNumber(stateTotal),
      hint: `${windowKey ? windowLabel(windowKey) : "Selected window"}${searchActive ? " · search applied" : " · before facet filters"}`,
    },
    {
      key: "succeeded",
      label: "Succeeded",
      value: formatNumber(succeeded),
      hint: successRate === null ? "No runs recorded" : `${formatPercent(successRate)} of window`,
      tone: "text-success",
    },
    {
      key: "not-succeeded",
      label: "Not succeeded",
      value: formatNumber(notSucceeded),
      hint: "Failed, cancelled, or security-stopped",
      tone: notSucceeded > 0 ? "text-warning" : undefined,
    },
    {
      key: "repositories",
      label: "Repositories",
      value: formatNumber((facets.repositories || []).length),
      hint: `${formatNumber((facets.harnesses || []).length)} harnesses, ${formatNumber((facets.scopes || []).length)} scopes`,
    },
    {
      key: "index",
      label: "Evidence index",
      value: index ? formatNumber(index.runs) : "—",
      hint: index
        ? `Refreshed ${timeAgo(index.refreshed_at)}${scanErrors ? ` · ${formatNumber(scanErrors)} scan errors` : ""}`
        : "Index metadata unavailable",
      tone: scanErrors > 0 ? "text-warning" : undefined,
    },
  ];

  return (
    <Card className="mb-4 overflow-hidden">
      <div className="grid grid-cols-2 gap-px bg-border sm:grid-cols-3 xl:grid-cols-5">
        {cells.map((cell) => (
          <div key={cell.key} className="min-w-0 bg-surface px-3.5 py-3">
            <p className="truncate text-[10px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground">
              {cell.label}
            </p>
            <p className={clsx("tabular mt-1 text-[18px] font-semibold leading-none tracking-[-0.02em]", cell.tone || "text-foreground")}>
              {cell.value}
            </p>
            <p className="mt-1.5 line-clamp-1 text-[11px] leading-4 text-muted-foreground" title={cell.hint}>
              {cell.hint}
            </p>
          </div>
        ))}
      </div>
    </Card>
  );
}
