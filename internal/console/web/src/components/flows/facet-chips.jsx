import clsx from "clsx";
import { formatNumber } from "@/lib/format";
import { StatusDot } from "@/components/ui/badge";

// FacetChips renders the multi-select status facets from the list response.
// Selection is additive (the API accepts repeated `status` parameters), and a
// zero-count facet stays visible but disabled so the filter surface never
// shifts under the operator.
export function FacetChips({ label, options = [], selected = [], onToggle, className, zeroHint = "No records with this value in the current window." }) {
  return (
    <div role="group" aria-label={label} className={clsx("flex flex-wrap items-center gap-1", className)}>
      {options.map((option) => {
        const active = selected.includes(option.value);
        const disabled = !active && Number(option.count) === 0;
        return (
          <button
            key={option.value}
            type="button"
            aria-pressed={active}
            disabled={disabled}
            onClick={() => onToggle?.(option.value)}
            title={disabled ? zeroHint : `${option.label}: ${formatNumber(option.count)}`}
            className={clsx(
              "inline-flex h-6 shrink-0 items-center gap-1.5 rounded-full border px-2 text-[11px] font-medium transition-colors",
              active
                ? "border-primary/40 bg-primary/10 text-primary"
                : "border-border bg-surface text-muted-foreground hover:border-border-strong hover:text-foreground",
              disabled && "pointer-events-none opacity-40",
            )}
          >
            <StatusDot tone={option.tone || "muted"} size="xs" />
            <span>{option.label}</span>
            <span
              className={clsx(
                "tabular text-[10px] font-semibold",
                active ? "text-primary/80" : "text-muted-foreground/80",
              )}
            >
              {formatNumber(option.count)}
            </span>
          </button>
        );
      })}
    </div>
  );
}
