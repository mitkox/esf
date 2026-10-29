import clsx from "clsx";

// Card is the base evidence surface: one hairline border, one radius, one very
// quiet inner highlight. Screens compose CardHeader / CardBody / CardFooter.
export function Card({ as: Tag = "section", className, children, ...rest }) {
  return (
    <Tag
      className={clsx(
        "evidence-card relative rounded-panel border border-border bg-surface",
        className,
      )}
      {...rest}
    >
      {children}
    </Tag>
  );
}

export function CardHeader({ title, description, actions, icon, className, children, bordered = true }) {
  return (
    <div
      className={clsx(
        "flex flex-wrap items-start gap-x-3 gap-y-2 px-4 py-4 sm:px-5",
        bordered && "border-b border-border",
        className,
      )}
    >
      {icon ? (
        <span className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground [&>svg]:h-3.5 [&>svg]:w-3.5">
          {icon}
        </span>
      ) : null}
      <div className="min-w-0 flex-1">
        {title ? (
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <h2 className="truncate text-sm font-semibold leading-5 tracking-[-0.01em] text-foreground">{title}</h2>
            {children}
          </div>
        ) : null}
        {description ? <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">{description}</p> : null}
      </div>
      {actions ? (
        <div className="flex shrink-0 flex-wrap items-center gap-1.5 sm:ml-auto sm:justify-end">{actions}</div>
      ) : null}
    </div>
  );
}

export function CardBody({ className, children, padded = true, ...rest }) {
  return (
    <div className={clsx(padded && "px-4 py-3.5 sm:px-5", className)} {...rest}>
      {children}
    </div>
  );
}

export function CardFooter({ className, children, ...rest }) {
  return (
    <div
      className={clsx(
        "flex flex-wrap items-center gap-2 border-t border-border px-4 py-2.5 text-[11px] text-muted-foreground sm:px-5",
        className,
      )}
      {...rest}
    >
      {children}
    </div>
  );
}
