import { useRef } from "react";
import clsx from "clsx";
import { formatNumber } from "@/lib/format";

// Tabs is a roving-focus tablist: ArrowLeft/ArrowRight/Home/End move selection
// and focus together, which is the pattern auditors expect from dense consoles.
export function Tabs({ tabs = [], value, onChange, className, size = "md" }) {
  const listRef = useRef(null);

  function move(event, index) {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    if (!tabs.length) return;
    let next = index;
    if (event.key === "ArrowLeft") next = (index - 1 + tabs.length) % tabs.length;
    if (event.key === "ArrowRight") next = (index + 1) % tabs.length;
    if (event.key === "Home") next = 0;
    if (event.key === "End") next = tabs.length - 1;
    const tab = tabs[next];
    if (!tab || tab.disabled) return;
    onChange?.(tab.id);
    listRef.current?.querySelectorAll('[role="tab"]')?.[next]?.focus();
  }

  return (
    <div
      ref={listRef}
      role="tablist"
      className={clsx(
        "scrollbar-none -mb-px flex items-center gap-1 overflow-x-auto border-b border-border",
        className,
      )}
    >
      {tabs.map((tab, index) => {
        const active = tab.id === value;
        return (
          <button
            key={tab.id}
            type="button"
            role="tab"
            aria-selected={active}
            tabIndex={active ? 0 : -1}
            disabled={tab.disabled}
            onClick={() => onChange?.(tab.id)}
            onKeyDown={(event) => move(event, index)}
            className={clsx(
              "relative inline-flex shrink-0 items-center gap-1.5 border-b-2 font-medium transition-colors disabled:opacity-45",
              size === "sm" ? "px-2 pb-2 pt-1 text-[11px]" : "px-2.5 pb-2.5 pt-1.5 text-[13px]",
              active
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:border-border-strong hover:text-foreground",
            )}
          >
            <span className="truncate">{tab.label}</span>
            {tab.count !== null && tab.count !== undefined && tab.count !== "" ? (
              <span
                className={clsx(
                  "tabular rounded-full px-1.5 text-[10px] font-semibold leading-4",
                  active ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground",
                )}
              >
                {typeof tab.count === "number" ? formatNumber(tab.count) : tab.count}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}
