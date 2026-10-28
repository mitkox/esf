import clsx from "clsx";
import { ArrowLeft } from "lucide-react";

// PageHeader is the single page-level title block. Screens pass actions as
// nodes (buttons, menus) and meta as chips/links; both wrap cleanly at 390px.
export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
  backHref,
  backLabel = "Back",
  meta,
  className,
}) {
  return (
    <header className={clsx("mb-5", className)}>
      {backHref ? (
        <a
          href={backHref}
          className="mb-3 inline-flex items-center gap-1.5 rounded-md text-[11px] font-medium text-muted-foreground transition-colors hover:text-foreground"
        >
          <ArrowLeft aria-hidden="true" className="h-3.5 w-3.5" />
          {backLabel}
        </a>
      ) : null}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          {eyebrow ? <p className="eyebrow">{eyebrow}</p> : null}
          <h1 className="text-[22px] font-semibold leading-tight tracking-[-0.02em] text-foreground sm:text-[26px]">
            {title}
          </h1>
          {description ? (
            <p className="mt-1.5 max-w-3xl text-[13px] leading-relaxed text-muted-foreground">{description}</p>
          ) : null}
          {meta ? <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-2 text-[11px] text-muted-foreground">{meta}</div> : null}
        </div>
        {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2 sm:justify-end">{actions}</div> : null}
      </div>
    </header>
  );
}
