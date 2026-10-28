import { useEffect, useRef } from "react";

// useDismiss wires one popover panel: outside pointerdown closes it, Escape
// closes it, and focus returns to the trigger when it closes (but never on the
// initial mount). Handlers are stored in refs so re-renders do not churn the
// document listeners.
export function useDismiss(open, onDismiss) {
  const panelRef = useRef(null);
  const triggerRef = useRef(null);
  const wasOpen = useRef(false);
  const dismissRef = useRef(onDismiss);

  useEffect(() => {
    dismissRef.current = onDismiss;
  }, [onDismiss]);

  useEffect(() => {
    if (!open) return undefined;
    const onPointerDown = (event) => {
      if (!panelRef.current || panelRef.current.contains(event.target)) return;
      dismissRef.current?.();
    };
    const onKeyDown = (event) => {
      if (event.key === "Escape") dismissRef.current?.();
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  useEffect(() => {
    if (wasOpen.current && !open) triggerRef.current?.focus?.();
    wasOpen.current = open;
  }, [open]);

  return { panelRef, triggerRef };
}
