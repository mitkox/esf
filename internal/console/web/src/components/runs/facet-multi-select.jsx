import { useEffect, useId, useRef, useState } from "react";
import clsx from "clsx";
import { Check, ChevronDown } from "lucide-react";
import { buttonStyles } from "@/components/ui/button";
import { StatusDot } from "@/components/ui/badge";
import { formatNumber } from "@/lib/format";

// FacetMultiSelect is a popover checkbox list for multi-value facets (run
// states). The trigger stays the same height as the surrounding native selects;
// the panel closes on outside click, Escape, or selection.
export function FacetMultiSelect({
  label,
  ariaLabel,
  options = [],
  values = [],
  onChange,
  toneFor,
  icon: Icon,
  className,
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef(null);
  const triggerRef = useRef(null);
  const panelID = useId();

  useEffect(() => {
    if (!open) return undefined;
    const onPointerDown = (event) => {
      if (!rootRef.current?.contains(event.target)) setOpen(false);
    };
    const onKeyDown = (event) => {
      if (event.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  const toggle = (value) => {
    onChange?.(values.includes(value) ? values.filter((item) => item !== value) : [...values, value]);
  };

  return (
    <div ref={rootRef} className={clsx("relative", className)}>
      <button
        ref={triggerRef}
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? panelID : undefined}
        aria-label={ariaLabel || `${label} filter`}
        onClick={() => setOpen((current) => !current)}
        className={buttonStyles({
          variant: "secondary",
          size: "sm",
          className: clsx("justify-between gap-2", values.length > 0 && "border-primary/40 text-primary"),
        })}
      >
        <span className="flex min-w-0 items-center gap-1.5">
          {Icon ? (
            <span className="shrink-0 [&>svg]:h-3.5 [&>svg]:w-3.5">
              <Icon aria-hidden="true" />
            </span>
          ) : null}
          <span className="truncate">{label}</span>
        </span>
        {values.length > 0 ? (
          <span className="tabular rounded-full bg-primary/15 px-1.5 text-[10px] font-semibold leading-4 text-primary">
            {values.length}
          </span>
        ) : null}
        <ChevronDown
          aria-hidden="true"
          className={clsx("h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-180")}
        />
      </button>

      {open ? (
        <div
          id={panelID}
          role="listbox"
          aria-label={`${label} filter values`}
          aria-multiselectable="true"
          className="absolute left-0 top-full z-50 mt-1.5 w-64 rounded-xl border border-border bg-surface-overlay p-1.5 shadow-popover"
        >
          <div className="flex items-center justify-between px-2 pb-1 pt-0.5">
            <span className="text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">{label}</span>
            {values.length > 0 ? (
              <button
                type="button"
                onClick={() => onChange?.([])}
                className="text-[11px] font-medium text-primary hover:underline"
              >
                Clear
              </button>
            ) : null}
          </div>
          <div className="scroll-thin max-h-72 overflow-y-auto">
            {options.length > 0 ? (
              options.map((option) => {
                const selected = values.includes(option.value);
                return (
                  <button
                    key={option.value}
                    type="button"
                    role="option"
                    aria-selected={selected}
                    onClick={() => toggle(option.value)}
                    className={clsx(
                      "flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[12px] transition-colors",
                      selected ? "bg-muted/60 text-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground",
                    )}
                  >
                    <span
                      aria-hidden="true"
                      className={clsx(
                        "grid h-4 w-4 shrink-0 place-items-center rounded border",
                        selected ? "border-primary bg-primary text-primary-foreground" : "border-border-strong bg-surface",
                      )}
                    >
                      {selected ? <Check className="h-3 w-3" /> : null}
                    </span>
                    {toneFor ? <StatusDot tone={toneFor(option.value)} size="xs" /> : null}
                    <span className="min-w-0 flex-1 truncate">{option.label}</span>
                    {option.count !== null && option.count !== undefined ? (
                      <span className="tabular shrink-0 text-[10px] text-muted-foreground">{formatNumber(option.count)}</span>
                    ) : null}
                  </button>
                );
              })
            ) : (
              <p className="px-2 py-3 text-[11px] text-muted-foreground">No values available.</p>
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}
