import { cloneElement, isValidElement } from "react";
import clsx from "clsx";
import { CircleAlert, CircleCheck, Info, ShieldAlert, TriangleAlert } from "lucide-react";
import { TONE_BORDER, TONE_SOFT, TONE_TEXT } from "@/components/ui/tones";

const ICONS = {
  info: Info,
  success: CircleCheck,
  warning: TriangleAlert,
  danger: CircleAlert,
  security: ShieldAlert,
};

// Notice is the shared inline message surface for honest states: stale data,
// unavailable live queries, read-only configuration and configuration warnings.
export function Notice({ tone = "info", title, children, icon, action, className, announce = false }) {
  const toneClass = clsx("mt-0.5 h-4 w-4 shrink-0", TONE_TEXT[tone] || TONE_TEXT.info);
  // Callers pass either an element (<Lock />) or a component reference (Lock),
  // because the same block is used from hand-written pages and data-driven
  // sections. Normalise both so an icon can never take the page down.
  const Icon = icon && !isValidElement(icon) ? icon : ICONS[tone] || Info;
  const iconNode = isValidElement(icon)
    ? cloneElement(icon, {
        "aria-hidden": true,
        className: clsx(toneClass, icon.props?.className),
      })
    : Icon
      ? <Icon aria-hidden="true" className={toneClass} />
      : null;
  return (
    <div
      role={tone === "danger" ? "alert" : "note"}
      aria-live={announce ? "polite" : undefined}
      className={clsx(
        "flex items-start gap-3 rounded-panel border px-4 py-3",
        TONE_BORDER[tone] || TONE_BORDER.info,
        TONE_SOFT[tone] || TONE_SOFT.info,
        className,
      )}
    >
      {iconNode}
      <div className="min-w-0 flex-1">
        {title ? <p className="text-[13px] font-semibold leading-5 text-foreground">{title}</p> : null}
        {children ? (
          <div className={clsx("break-words text-xs leading-relaxed text-muted-foreground", title && "mt-0.5")}>
            {children}
          </div>
        ) : null}
      </div>
      {action ? <div className="shrink-0 self-center">{action}</div> : null}
    </div>
  );
}
