import clsx from "clsx";
import { TONE_TEXT } from "@/components/ui/tones";
import { Card } from "@/components/ui/card";

// StatStrip is the compact metric rail shared by the portfolio and the review
// queue. A hairline is drawn by a 1px gap over the border colour, so the cells
// keep their separators at every responsive breakpoint.
export function StatStripSkeleton({
  items = 6,
  columnsClassName = "grid-cols-2 sm:grid-cols-3 xl:grid-cols-6",
  className,
}) {
  return (
    <Card className={clsx("overflow-hidden", className)} role="status" aria-label="Loading summary metrics">
      <div className={clsx("grid gap-px bg-border", columnsClassName)}>
        {Array.from({ length: items }).map((_, index) => (
          <div key={index} className="bg-surface px-3 py-2.5 sm:px-3.5">
            <div className="skeleton h-2 w-16" />
            <div className="skeleton mt-2 h-3.5 w-12" />
          </div>
        ))}
      </div>
    </Card>
  );
}

export function StatStrip({ items = [], columnsClassName = "grid-cols-2 sm:grid-cols-3 xl:grid-cols-6", className, label }) {
  return (
    <Card
      as="section"
      className={clsx("overflow-hidden", className)}
      aria-label={label || "Summary metrics"}
      role="region"
    >
      <div className={clsx("grid gap-px bg-border", columnsClassName)}>
        {items.map((item, index) => {
          const tone = TONE_TEXT[item.tone] ? item.tone : "";
          return (
            <div key={item.key || `${item.label}-${index}`} className="min-w-0 bg-surface px-3 py-2.5 sm:px-3.5">
              <div className="flex min-w-0 items-center gap-1.5">
                {item.icon ? (
                  <span
                    aria-hidden="true"
                    className={clsx("shrink-0 [&>svg]:h-3 [&>svg]:w-3", tone ? TONE_TEXT[tone] : "text-muted-foreground")}
                  >
                    {item.icon}
                  </span>
                ) : null}
                <p
                  className="min-w-0 truncate text-[10px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground"
                  title={item.hint || undefined}
                >
                  {item.label}
                </p>
              </div>
              <div
                className={clsx(
                  "mt-1 flex min-w-0 items-baseline gap-1.5 text-[15px] font-semibold leading-6 tracking-[-0.01em]",
                  item.muted ? "text-muted-foreground" : tone ? TONE_TEXT[tone] : "text-foreground",
                )}
              >
                <span className="tabular min-w-0 truncate">{item.value}</span>
                {item.valueSuffix ? (
                  <span className="shrink-0 text-[10.5px] font-medium uppercase tracking-[0.06em] text-muted-foreground">
                    {item.valueSuffix}
                  </span>
                ) : null}
              </div>
              {item.sub ? (
                <p className="mt-0.5 min-w-0 truncate text-[10.5px] leading-4 text-muted-foreground" title={item.subTitle || undefined}>
                  {item.sub}
                </p>
              ) : null}
            </div>
          );
        })}
      </div>
    </Card>
  );
}
