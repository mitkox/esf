import clsx from "clsx";
import { TONE_BORDER, TONE_SOFT, TONE_TEXT } from "@/components/ui/tones";

// Notice is the inline, non-modal message used for warnings, honest "not
// reported" explanations and live-availability notes. It is deliberately not
// a toast: these messages stay attached to the evidence they qualify.
export function Notice({ tone = "warning", icon, title, children, action, className, role = "note" }) {
  const resolved = TONE_TEXT[tone] ? tone : "warning";
  return (
    <div
      role={role}
      className={clsx("flex items-start gap-2.5 rounded-panel border px-3.5 py-2.5", TONE_BORDER[resolved], TONE_SOFT[resolved], className)}
    >
      {icon ? (
        <span className={clsx("mt-0.5 shrink-0 [&>svg]:h-3.5 [&>svg]:w-3.5", TONE_TEXT[resolved])} aria-hidden="true">
          {icon}
        </span>
      ) : null}
      <div className="min-w-0 flex-1">
        {title ? <p className="text-xs font-semibold leading-5 text-foreground">{title}</p> : null}
        {children ? (
          <div className={clsx("break-words text-[11.5px] leading-relaxed text-muted-foreground", title && "mt-0.5")}>{children}</div>
        ) : null}
      </div>
      {action ? <div className="shrink-0 self-center">{action}</div> : null}
    </div>
  );
}
