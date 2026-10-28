import clsx from "clsx";
import { EmptyState } from "./empty-state";

const ALIGN = {
  left: "text-left",
  center: "text-center",
  right: "text-right",
};

export function Table({ className, tableClassName, children, minWidth, ...rest }) {
  return (
    <div className={clsx("scroll-thin w-full overflow-x-auto", className)}>
      <table
        {...rest}
        className={clsx("w-full border-collapse text-left text-[13px]", tableClassName)}
        style={minWidth ? { minWidth } : undefined}
      >
        {children}
      </table>
    </div>
  );
}

export function THead({ className, children, ...rest }) {
  return (
    <thead className={clsx("sticky top-0 z-10 bg-surface-raised", className)} {...rest}>
      {children}
    </thead>
  );
}

export function TH({ className, children, align = "left", numeric = false, ...rest }) {
  return (
    <th
      scope="col"
      className={clsx(
        "whitespace-nowrap border-b border-border px-3 py-2 text-[10px] font-semibold uppercase leading-4 tracking-[0.1em] text-muted-foreground",
        ALIGN[numeric ? "right" : align] || ALIGN.left,
        className,
      )}
      {...rest}
    >
      {children}
    </th>
  );
}

export function TR({ className, children, ...rest }) {
  return (
    <tr
      className={clsx(
        "border-b border-border/80 transition-colors last:border-b-0 hover:bg-surface-raised/60",
        className,
      )}
      {...rest}
    >
      {children}
    </tr>
  );
}

export function TD({ className, children, align, numeric = false, muted = false, ...rest }) {
  return (
    <td
      className={clsx(
        "px-3 py-2.5 align-middle",
        ALIGN[numeric ? "right" : align] || ALIGN.left,
        numeric && "tabular",
        muted ? "text-muted-foreground" : "text-foreground",
        className,
      )}
      {...rest}
    >
      {children}
    </td>
  );
}

export function TableEmpty({ colSpan = 1, title = "No results", description, action, icon, children, className }) {
  return (
    <tr className={clsx("hover:bg-transparent", className)}>
      <td colSpan={colSpan} className="px-3 py-8">
        {children ? (
          <div className="text-center text-[13px] text-muted-foreground">{children}</div>
        ) : (
          <EmptyState compact icon={icon} title={title} description={description} action={action} />
        )}
      </td>
    </tr>
  );
}

export function TableToolbar({ children, actions, className, ...rest }) {
  return (
    <div
      className={clsx(
        "flex flex-wrap items-center gap-x-2 gap-y-2 border-b border-border bg-surface px-3 py-2",
        className,
      )}
      {...rest}
    >
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">{children}</div>
      {actions ? <div className="flex flex-wrap items-center gap-2 sm:ml-auto sm:justify-end">{actions}</div> : null}
    </div>
  );
}
