import clsx from "clsx";

const TEXT_WIDTHS = ["100%", "92%", "78%", "86%", "64%", "71%"];
const ROW_WIDTHS = [
  ["28%", "18%", "22%", "14%", "18%"],
  ["34%", "12%", "18%", "16%", "20%"],
  ["24%", "20%", "26%", "12%", "18%"],
];

export function Skeleton({ className, ...rest }) {
  return <div aria-hidden="true" className={clsx("skeleton", className)} {...rest} />;
}

export function SkeletonText({ lines = 3, className, lineClassName }) {
  const count = Math.max(1, lines);
  return (
    <div className={clsx("space-y-2", className)} aria-hidden="true">
      {Array.from({ length: count }).map((_, index) => (
        <div
          key={index}
          className={clsx("skeleton h-2.5", lineClassName)}
          style={{ width: index === count - 1 && count > 1 ? TEXT_WIDTHS[2] : TEXT_WIDTHS[index % TEXT_WIDTHS.length] }}
        />
      ))}
    </div>
  );
}

function columnWidth(index, columns) {
  if (index === 0) return "26%";
  if (index === columns - 1) return "12%";
  return `${Math.max(12, Math.round(56 / Math.max(1, columns - 2)))}%`;
}

export function TableSkeleton({ rows = 6, columns = 5, className, showHeader = true }) {
  return (
    <div className={clsx("overflow-hidden rounded-panel border border-border bg-surface", className)} role="status" aria-label="Loading table">
      {showHeader ? (
        <div className="flex items-center gap-4 border-b border-border bg-surface-raised px-4 py-2.5">
          {Array.from({ length: columns }).map((_, index) => (
            <Skeleton key={index} className="h-2 w-14 shrink-0" />
          ))}
        </div>
      ) : null}
      <div className="divide-y divide-border">
        {Array.from({ length: rows }).map((_, rowIndex) => (
          <div key={rowIndex} className="flex items-center gap-4 px-4 py-3">
            {Array.from({ length: columns }).map((_, columnIndex) => (
              <Skeleton
                key={columnIndex}
                className="h-2.5 shrink-0"
                style={{ width: columnWidth(columnIndex, columns) }}
              />
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}

const BAR_HEIGHTS = [42, 58, 35, 72, 64, 80, 56, 68, 48, 76, 60, 84];

export function ChartSkeleton({ className, height = 220 }) {
  return (
    <div className={clsx("rounded-panel border border-border bg-surface p-4", className)} role="status" aria-label="Loading chart">
      <Skeleton className="h-3 w-36" />
      <div className="mt-4 flex items-end gap-2" style={{ height }}>
        {BAR_HEIGHTS.map((bar, index) => (
          <Skeleton key={index} className="min-w-0 flex-1 rounded-t-sm rounded-b-none" style={{ height: `${bar}%` }} />
        ))}
      </div>
      <div className="mt-3 flex items-center gap-3">
        <Skeleton className="h-2.5 w-24" />
        <Skeleton className="h-2.5 w-16" />
        <Skeleton className="h-2.5 w-12" />
      </div>
    </div>
  );
}

export function KpiSkeleton({ className }) {
  return (
    <div className={clsx("rounded-panel border border-border bg-surface p-4", className)} role="status" aria-label="Loading metric">
      <div className="flex items-start justify-between gap-3">
        <Skeleton className="h-2.5 w-24" />
        <Skeleton className="h-8 w-8 rounded-lg" />
      </div>
      <Skeleton className="mt-3 h-7 w-20" />
      <Skeleton className="mt-2 h-2.5 w-32" />
      <Skeleton className="mt-4 h-8 w-full" />
    </div>
  );
}
