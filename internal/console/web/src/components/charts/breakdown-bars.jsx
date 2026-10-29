import clsx from "clsx";
import { formatDuration, formatNumber, formatPercent, formatUsd } from "@/lib/format";

// BreakdownBars is the shared ranked-bar list used for harness, repository and
// scope breakdowns. The bar length encodes run volume; success rate and p50
// duration stay as text so the ranking is never ambiguous.
export function BreakdownBars({
  items = [],
  labelOf = (item) => item.name,
  emptyLabel = "No activity in this window.",
  showCost,
  className,
}) {
  if (!items.length) {
    return <p className={clsx("py-6 text-center text-[11px] text-muted-foreground", className)}>{emptyLabel}</p>;
  }

  const maxRuns = Math.max(1, ...items.map((item) => Number(item.runs) || 0));
  const withCost = showCost === undefined ? items.some((item) => Number(item.cost_usd) > 0) : showCost;
  const columns = withCost
    ? "grid-cols-[minmax(0,1fr)_3rem_3.5rem_3.75rem_4.5rem]"
    : "grid-cols-[minmax(0,1fr)_3rem_3.5rem_3.75rem]";

  return (
    <div className={className}>
      <div
        className={clsx(
          "grid items-center gap-2 border-b border-border pb-1.5 text-[9px] font-semibold uppercase leading-4 tracking-[0.1em] text-muted-foreground",
          columns,
        )}
      >
        <span>Name</span>
        <span className="text-right">Runs</span>
        <span className="text-right">Success</span>
        <span className="text-right">p50</span>
        {withCost ? <span className="text-right">Cost</span> : null}
      </div>
      <ul className="divide-y divide-border/70">
        {items.map((item, index) => {
          const runs = Number(item.runs) || 0;
          const share = runs / maxRuns;
          const label = labelOf(item);
          const success = Number(item.success_rate);
          return (
            <li key={`${item.name || "row"}-${index}`} className={clsx("grid items-center gap-2 py-[7px]", columns)}>
              <div className="relative min-w-0 rounded-sm">
                <span
                  aria-hidden="true"
                  className="absolute inset-y-0 left-0 rounded-sm bg-primary/10"
                  style={{ width: `${Math.max(share * 100, 1.5)}%` }}
                />
                <span
                  className="relative block truncate pl-1 text-[12px] font-medium leading-4 text-foreground"
                  title={label}
                >
                  {label}
                </span>
              </div>
              <span className="tabular text-right text-[12px] leading-4 text-foreground">{formatNumber(runs)}</span>
              <span
                className={clsx(
                  "tabular text-right text-[11px] leading-4",
                  Number.isFinite(success) && success < 0.5 && runs > 0
                    ? "font-medium text-warning"
                    : "text-muted-foreground",
                )}
                title={Number.isFinite(success) ? `${formatPercent(success)} of finished runs succeeded` : "No finished runs"}
              >
                {runs > 0 ? formatPercent(item.success_rate, { fallback: "—" }) : "—"}
              </span>
              <span className="tabular text-right text-[11px] leading-4 text-muted-foreground">
                {Number(item.p50_duration_ms) > 0 ? formatDuration(item.p50_duration_ms) : "—"}
              </span>
              {withCost ? (
                <span className="tabular text-right text-[11px] leading-4 text-muted-foreground">
                  {Number(item.cost_usd) > 0 ? formatUsd(item.cost_usd) : "—"}
                </span>
              ) : null}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
