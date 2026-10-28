import { useEffect, useRef } from "react";
import { Keyboard, X } from "lucide-react";
import { NAV_GROUPS } from "@/routes";
import { NAV_SHORTCUTS } from "./nav-icons";
import { modifierKeyLabel } from "./platform";

function ShortcutRow({ label, keys }) {
  return (
    <div className="flex items-center justify-between gap-3 py-1">
      <span className="text-[11px] leading-4 text-muted-foreground">{label}</span>
      <span className="flex shrink-0 items-center gap-1">
        {keys.map((key, index) => (
          <span key={`${key}-${index}`} className="kbd" aria-hidden="true">
            {key}
          </span>
        ))}
      </span>
    </div>
  );
}

export function ShortcutsDialog({ open, onClose }) {
  const closeRef = useRef(null);

  useEffect(() => {
    if (!open) return undefined;
    const previous = document.activeElement;
    closeRef.current?.focus?.();
    return () => {
      if (previous instanceof HTMLElement) previous.focus();
    };
  }, [open]);

  useEffect(() => {
    if (!open) return undefined;
    const onKeyDown = (event) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose?.();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open, onClose]);

  if (!open) return null;
  const modifier = modifierKeyLabel();

  return (
    <div className="fixed inset-0 z-[75] flex items-center justify-center px-3 py-6">
      <div aria-hidden="true" onClick={onClose} className="dialog-overlay absolute inset-0 bg-black/40 backdrop-blur-[2px] dark:bg-black/60" />
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="shortcuts-title"
        className="dialog-panel relative flex max-h-full w-full max-w-md flex-col overflow-hidden rounded-panel border border-border-strong bg-surface-overlay shadow-popover"
      >
        <div className="flex items-center gap-2.5 border-b border-border px-4 py-3">
          <span className="grid h-7 w-7 place-items-center rounded-lg border border-border bg-surface-raised text-muted-foreground">
            <Keyboard aria-hidden="true" className="h-3.5 w-3.5" />
          </span>
          <h2 id="shortcuts-title" className="text-[13px] font-semibold text-foreground">
            Keyboard shortcuts
          </h2>
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            aria-label="Close keyboard shortcuts"
            className="ml-auto grid h-7 w-7 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <X aria-hidden="true" className="h-3.5 w-3.5" />
          </button>
        </div>

        <div className="scroll-thin flex-1 overflow-y-auto px-4 py-3">
          <p className="text-[10px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground">
            General
          </p>
          <div className="mt-1 divide-y divide-border/60">
            <ShortcutRow label="Command palette" keys={[modifier, "K"]} />
            <ShortcutRow label="Search runs and changes" keys={["/"]} />
            <ShortcutRow label="Show this reference" keys={["?"]} />
            <ShortcutRow label="Close overlay or menu" keys={["Esc"]} />
          </div>

          <p className="mt-4 text-[10px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground">
            Navigate
          </p>
          <div className="mt-1 divide-y divide-border/60">
            {NAV_GROUPS.flatMap((group) =>
              group.items.map((item) => (
                <ShortcutRow
                  key={item.id}
                  label={item.label}
                  keys={["g", NAV_SHORTCUTS[item.id] || "?"]}
                />
              )),
            )}
          </div>

          <p className="mt-4 text-[10px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground">
            Command palette
          </p>
          <div className="mt-1 divide-y divide-border/60">
            <ShortcutRow label="Move selection" keys={["↑", "↓"]} />
            <ShortcutRow label="Open selected result" keys={["↵"]} />
            <ShortcutRow label="Jump to first / last" keys={["Home", "End"]} />
          </div>
        </div>

        <div className="border-t border-border bg-surface/60 px-4 py-2 text-[10px] leading-4 text-muted-foreground">
          Navigation shortcuts pause while you are typing in a field.
        </div>
      </div>
    </div>
  );
}
