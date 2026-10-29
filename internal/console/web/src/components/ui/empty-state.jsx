import clsx from "clsx";

export function EmptyState({ icon, title, description, action, compact = false, className }) {
  return (
    <div
      className={clsx(
        "flex flex-col items-center justify-center px-4 text-center",
        compact ? "gap-2 py-8" : "gap-3 py-14",
        className,
      )}
    >
      {icon ? (
        <div className="grid h-10 w-10 place-items-center rounded-xl border border-border bg-surface-raised text-muted-foreground [&>svg]:h-4 [&>svg]:w-4">
          {icon}
        </div>
      ) : null}
      <div className="max-w-sm">
        {title ? <p className="text-[13px] font-semibold text-foreground">{title}</p> : null}
        {description ? (
          <p className={clsx("text-[11px] leading-relaxed text-muted-foreground", title && "mt-1")}>{description}</p>
        ) : null}
      </div>
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  );
}
