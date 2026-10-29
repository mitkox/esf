import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import clsx from "clsx";
import { CircleCheck, CircleX, Info, ShieldAlert, TriangleAlert, X } from "lucide-react";

const ToastContext = createContext(null);
let nextID = 1;

const TONES = {
  default: { Icon: Info, iconClass: "text-info" },
  info: { Icon: Info, iconClass: "text-info" },
  success: { Icon: CircleCheck, iconClass: "text-success" },
  warning: { Icon: TriangleAlert, iconClass: "text-warning" },
  danger: { Icon: CircleX, iconClass: "text-danger" },
  security: { Icon: ShieldAlert, iconClass: "text-security" },
};

function ToastItem({ item, onDismiss }) {
  const tone = TONES[item.tone] || TONES.default;
  const Icon = tone.Icon;

  useEffect(() => {
    if (!item.duration) return undefined;
    const timer = window.setTimeout(() => onDismiss(item.id), item.duration);
    return () => window.clearTimeout(timer);
  }, [item.duration, item.id, onDismiss]);

  return (
    <div
      role={item.tone === "danger" ? "alert" : "status"}
      className="toast-enter pointer-events-auto flex w-full items-start gap-2.5 rounded-xl border border-border bg-surface-overlay px-3.5 py-3 shadow-popover"
    >
      <Icon aria-hidden="true" className={clsx("mt-0.5 h-4 w-4 shrink-0", tone.iconClass)} />
      <div className="min-w-0 flex-1">
        {item.title ? <p className="text-[13px] font-semibold leading-5 text-foreground">{item.title}</p> : null}
        {item.message ? <p className="break-words text-xs leading-relaxed text-muted-foreground">{item.message}</p> : null}
      </div>
      <button
        type="button"
        onClick={() => onDismiss(item.id)}
        aria-label="Dismiss notification"
        className="-mr-1 -mt-1 grid h-6 w-6 shrink-0 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      >
        <X aria-hidden="true" className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}

export function ToastProvider({ children }) {
  const [toasts, setToasts] = useState([]);

  const dismiss = useCallback((id) => {
    setToasts((current) => current.filter((item) => item.id !== id));
  }, []);

  // toast("Saved") and toast({ title, message, tone }) are both accepted so
  // older call sites keep working while richer call sites stay readable.
  const toast = useCallback((message, options = {}) => {
    const id = nextID++;
    const input = message && typeof message === "object" ? message : { message, ...options };
    const entry = {
      id,
      tone: input.tone || "default",
      title: input.title ? String(input.title) : "",
      message: input.message ? String(input.message) : "",
      duration: input.duration ?? 4200,
    };
    setToasts((current) => [...current.slice(-3), entry]);
    return id;
  }, []);

  const value = useMemo(() => ({ toast, dismiss }), [toast, dismiss]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        aria-live="polite"
        aria-atomic="false"
        className="pointer-events-none fixed inset-x-3 bottom-3 z-[90] flex flex-col items-end gap-2 pb-[env(safe-area-inset-bottom)] sm:inset-x-auto sm:bottom-4 sm:right-4"
      >
        {toasts.map((item) => (
          <div key={item.id} className="w-full max-w-sm">
            <ToastItem item={item} onDismiss={dismiss} />
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast() {
  const value = useContext(ToastContext);
  if (!value) throw new Error("useToast must be used inside ToastProvider");
  return value.toast;
}
