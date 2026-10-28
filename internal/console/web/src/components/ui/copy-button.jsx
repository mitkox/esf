import { useEffect, useRef, useState } from "react";
import clsx from "clsx";
import { Check, Copy } from "lucide-react";
import { buttonStyles } from "./button";

// CopyButton copies identifiers without a modal or toast; it confirms inline
// for 1.6s. Clipboard failures fall back to execCommand and otherwise stay
// quiet, because losing a copy is not worth interrupting an audit session.
export function CopyButton({ value, label, size = "sm", variant = "ghost", className }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef(0);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  async function copy(event) {
    event?.preventDefault();
    event?.stopPropagation();
    const text = String(value ?? "");
    if (!text) return;
    let ok = false;
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
        ok = true;
      }
    } catch {
      ok = false;
    }
    if (!ok) {
      try {
        const area = document.createElement("textarea");
        area.value = text;
        area.setAttribute("readonly", "");
        area.style.position = "fixed";
        area.style.top = "-1000px";
        area.style.opacity = "0";
        document.body.appendChild(area);
        area.select();
        ok = document.execCommand("copy");
        document.body.removeChild(area);
      } catch {
        ok = false;
      }
    }
    if (!ok) return;
    setCopied(true);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopied(false), 1600);
  }

  const iconSize = size === "xs" ? "icon-xs" : size === "md" ? "icon" : "icon-sm";
  const Icon = copied ? Check : Copy;
  return (
    <button
      type="button"
      onClick={copy}
      title={copied ? "Copied" : `Copy ${label || "value"}`}
      aria-label={copied ? "Copied" : `Copy ${label || "value"}`}
      className={buttonStyles({
        variant,
        size: iconSize,
        className: clsx(
          "text-muted-foreground hover:text-foreground",
          copied && "text-success hover:text-success",
          className,
        ),
      })}
    >
      <Icon aria-hidden="true" className={size === "xs" ? "h-3 w-3" : "h-3.5 w-3.5"} />
      <span role="status" aria-live="polite" className="sr-only">
        {copied ? "Copied to clipboard" : ""}
      </span>
    </button>
  );
}
