import { useEffect, useId, useRef, useState } from "react";
import clsx from "clsx";
import { AlertTriangle, X } from "lucide-react";
import { api } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { Button, buttonStyles } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { EmptyState } from "@/components/ui/empty-state";
import { Spinner } from "@/components/ui/spinner";
import { formatBytes } from "@/lib/format";
import { artifactKindLabel, artifactKindVariant } from "./utils";

// Modal is the shared page-level evidence dialog. It locks background scroll,
// restores focus on close, closes on Escape/backdrop and keeps Tab inside the
// panel. The page-level z-index is above the shell chrome and its menus.
export function Modal({
  open,
  title,
  description,
  onClose,
  children,
  actions,
  footer,
  maxWidth = "max-w-4xl",
  labelledBy,
}) {
  const panelRef = useRef(null);
  const previousFocus = useRef(null);
  const generatedID = useId();
  const titleID = labelledBy || generatedID;

  useEffect(() => {
    if (!open) return undefined;
    previousFocus.current = document.activeElement;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const focusTimer = window.setTimeout(() => panelRef.current?.focus(), 0);
    const onKeyDown = (event) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        onClose?.();
        return;
      }
      if (event.key !== "Tab" || !panelRef.current) return;
      const focusable = panelRef.current.querySelectorAll(
        'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
      );
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (document.activeElement === panelRef.current) {
        event.preventDefault();
        (event.shiftKey ? last : first).focus();
        return;
      }
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      window.clearTimeout(focusTimer);
      document.body.style.overflow = previousOverflow;
      previousFocus.current?.focus?.();
    };
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-[85] flex items-end justify-center sm:items-center sm:p-4">
      <div aria-hidden="true" className="dialog-overlay absolute inset-0 bg-black/60" onClick={onClose} />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleID}
        tabIndex={-1}
        className={clsx(
          "dialog-panel relative flex max-h-[92vh] w-full flex-col overflow-hidden rounded-t-2xl border border-border bg-surface-overlay shadow-popover outline-none sm:rounded-panel",
          maxWidth,
        )}
      >
        <div className="flex items-start gap-3 border-b border-border px-4 py-3 sm:px-5">
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <h2 id={titleID} className="min-w-0 truncate text-[13px] font-semibold leading-5 text-foreground">
                {title}
              </h2>
            </div>
            {description ? (
              <p className="mt-0.5 truncate text-[11px] leading-4 text-muted-foreground" title={description}>
                {description}
              </p>
            ) : null}
          </div>
          {actions ? <div className="flex shrink-0 items-center gap-1.5">{actions}</div> : null}
          <button
            type="button"
            onClick={onClose}
            aria-label="Close dialog"
            className={buttonStyles({ variant: "ghost", size: "icon-sm" })}
          >
            <X aria-hidden="true" className="h-3.5 w-3.5" />
          </button>
        </div>
        <div className="scroll-thin min-h-0 flex-1 overflow-auto">{children}</div>
        {footer ? <div className="border-t border-border px-4 py-2.5 sm:px-5">{footer}</div> : null}
      </div>
    </div>
  );
}

// ArtifactPreviewModal fetches one allowlisted evidence file through the
// console API. The server returns JSON with a bounded text body; binary files
// are rejected server-side and rendered as an honest error state.
export function ArtifactPreviewModal({ runID, path, label, onClose }) {
  const open = Boolean(runID && path);
  const [state, setState] = useState({ loading: true, error: null, data: null });
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    if (!open) return undefined;
    let cancelled = false;
    const controller = new AbortController();
    setState({ loading: true, error: null, data: null });
    api
      .get(`/runs/${encodeURIComponent(runID)}/artifacts`, { path }, { signal: controller.signal })
      .then((data) => {
        if (!cancelled) setState({ loading: false, error: null, data });
      })
      .catch((error) => {
        if (!cancelled && error?.name !== "AbortError") setState({ loading: false, error, data: null });
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [open, runID, path, nonce]);

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={<span className="font-mono text-[12px]">{path || "Evidence"}</span>}
      description={label && label !== path ? `${label} · ${runID}` : `Run ${runID}`}
      actions={
        state.data?.content ? <CopyButton value={state.data.content} label="evidence content" size="sm" /> : null
      }
      footer={
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="text-[11px] text-muted-foreground">Evidence previews are bounded and read-only.</span>
          <div className="flex items-center gap-2">
            {state.error ? (
              <Button variant="secondary" size="sm" onClick={() => setNonce((value) => value + 1)}>
                Retry
              </Button>
            ) : null}
            <Button variant="secondary" size="sm" onClick={onClose}>
              Close
            </Button>
          </div>
        </div>
      }
    >
      {state.loading ? (
        <div className="flex items-center gap-2 px-4 py-8 text-[12px] text-muted-foreground sm:px-5">
          <Spinner size="sm" />
          Loading evidence…
        </div>
      ) : state.error ? (
        <EmptyState
          icon={<AlertTriangle aria-hidden="true" />}
          title="Evidence could not be previewed"
          description={state.error.message || "The evidence file could not be read."}
        />
      ) : (
        <div>
          <div className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-2 text-[11px] text-muted-foreground sm:px-5">
            <span className="min-w-0 truncate font-mono text-foreground" title={state.data?.path}>
              {state.data?.path}
            </span>
            <Badge variant={artifactKindVariant(state.data?.kind)} size="xs">
              {artifactKindLabel(state.data?.kind)}
            </Badge>
            <span className="tabular">{formatBytes(state.data?.bytes)}</span>
            {state.data?.truncated ? (
              <Badge variant="warning" size="xs">
                Truncated preview
              </Badge>
            ) : null}
          </div>
          {state.data?.content ? (
            <pre className="scroll-thin max-h-[58vh] overflow-auto whitespace-pre-wrap break-words px-4 py-3 font-mono text-[11.5px] leading-relaxed text-foreground sm:px-5">
              {state.data.content}
            </pre>
          ) : (
            <div className="px-4 py-8 text-center text-[12px] text-muted-foreground sm:px-5">
              This evidence file is empty.
            </div>
          )}
        </div>
      )}
    </Modal>
  );
}
