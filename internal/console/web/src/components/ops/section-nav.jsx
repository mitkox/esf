import { useEffect, useState } from "react";
import clsx from "clsx";

// SectionNav keeps the long read-only settings surface navigable. It uses
// buttons + scrollIntoView rather than anchors because the console is a hash
// router: an href="#id" would navigate away instead of scrolling.
export function SectionNav({ items = [], label = "Sections", className }) {
  const [active, setActive] = useState(() => items[0]?.id || "");
  const itemKey = items.map((item) => item.id).join("|");

  useEffect(() => {
    if (!items.length) return undefined;
    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((entry) => entry.isIntersecting && entry.target.id)
          .sort((a, b) => b.intersectionRatio - a.intersectionRatio);
        if (visible[0]) setActive(visible[0].target.id);
      },
      { rootMargin: "-150px 0px -55% 0px", threshold: [0, 0.25, 0.5, 1] },
    );
    for (const item of items) {
      const element = document.getElementById(item.id);
      if (element) observer.observe(element);
    }
    return () => observer.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [itemKey]);

  function jump(id) {
    const element = document.getElementById(id);
    if (!element) return;
    const reduceMotion = window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches;
    element.scrollIntoView({ behavior: reduceMotion ? "auto" : "smooth", block: "start" });
    setActive(id);
  }

  if (!items.length) return null;
  return (
    <nav
      aria-label={label}
      className={clsx(
        "chrome-blur scrollbar-none sticky top-[var(--topbar-height)] z-30 -mx-4 mb-4 overflow-x-auto border-b border-border px-4 py-2 sm:-mx-6 sm:px-6 lg:-mx-8 lg:px-8",
        className,
      )}
    >
      <div className="flex items-center gap-1">
        {items.map((item) => {
          const current = item.id === active;
          return (
            <button
              key={item.id}
              type="button"
              onClick={() => jump(item.id)}
              aria-current={current ? "true" : undefined}
              className={clsx(
                "inline-flex shrink-0 items-center whitespace-nowrap rounded-full border px-2.5 py-1 text-[11px] font-medium leading-4 transition-colors",
                current
                  ? "border-primary/30 bg-primary/10 text-primary"
                  : "border-transparent text-muted-foreground hover:bg-muted hover:text-foreground",
              )}
            >
              {item.label}
            </button>
          );
        })}
      </div>
    </nav>
  );
}
