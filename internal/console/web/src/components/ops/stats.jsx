import clsx from "clsx";
import { formatNumber } from "@/lib/format";
import { TONE_TEXT } from "@/components/ui/tones";
import { asNumber } from "./data";

const COLUMNS = {
  2: "grid-cols-2",
  3: "grid-cols-2 sm:grid-cols-3",
  4: "grid-cols-2 lg:grid-cols-4",
  5: "grid-cols-2 sm:grid-cols-3 xl:grid-cols-5",
  6: "grid-cols-2 sm:grid-cols-3 xl:grid-cols-6",
};

function displayValue(value) {
  if (value === null || value === undefined || value === "") return "—";
  const number = asNumber(value);
  return number === null ? value : formatNumber(number);
}

// StatStrip is a hairline-divided metric band that sits directly under a
// CardHeader (it supplies its own gap-px separators, not card padding).
export function StatStrip({ items = [], columns = 4, className }) {
  return (
    <div className={clsx("grid gap-px bg-border", COLUMNS[columns] || COLUMNS[4], className)}>
      {items.filter(Boolean).map((item, index) => {
        const tone = TONE_TEXT[item.tone] ? item.tone : "muted";
        return (
          <div key={`${item.label}:${index}`} className="min-w-0 bg-surface px-4 py-3.5">
            <p className="truncate text-[10px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground">
              {item.label}
            </p>
            <div className="mt-1 flex flex-wrap items-baseline gap-x-1.5 gap-y-0.5">
              <span
                className={clsx(
                  "tabular text-[20px] font-semibold leading-tight tracking-[-0.02em]",
                  TONE_TEXT[tone],
                )}
              >
                {displayValue(item.value)}
              </span>
              {item.suffix ? <span className="text-[11px] text-muted-foreground">{item.suffix}</span> : null}
            </div>
            {item.hint ? (
              <p className="mt-0.5 line-clamp-2 text-[11px] leading-snug text-muted-foreground">{item.hint}</p>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}
