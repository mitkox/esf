import clsx from "clsx";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";

// ConfigSection is a titled card with a stable scroll id, used by the settings
// page (and any operations card that reads like configuration). The id allows
// the section nav to jump without touching the hash router.
export function ConfigSection({
  id,
  title,
  description,
  icon,
  actions,
  children,
  footer,
  className,
  bodyClassName,
  padded = true,
}) {
  return (
    <Card id={id} className={clsx("scroll-mt-28", className)}>
      <CardHeader title={title} description={description} icon={icon} actions={actions} />
      {padded ? <CardBody className={bodyClassName}>{children}</CardBody> : <div className={bodyClassName}>{children}</div>}
      {footer ? <CardFooter>{footer}</CardFooter> : null}
    </Card>
  );
}

// CardSubsection is a secondary band inside a card: a small caps title row
// followed by a table or list that needs no extra horizontal padding.
export function CardSubsection({ title, description, actions, children, className }) {
  return (
    <div className={clsx("border-t border-border", className)}>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 px-4 py-2.5 sm:px-5">
        <h3 className="text-[10px] font-semibold uppercase leading-4 tracking-[0.1em] text-muted-foreground">
          {title}
        </h3>
        {description ? <p className="text-[11px] leading-snug text-muted-foreground/80">{description}</p> : null}
        {actions ? <div className="flex flex-wrap items-center gap-1.5 sm:ml-auto">{actions}</div> : null}
      </div>
      {children}
    </div>
  );
}

// ConfigPanel is a bordered sub-panel used to group the redacted infrastructure
// endpoints (Cube / Temporal / Observability / Storage) inside one section.
export function ConfigPanel({ title, description, children, className }) {
  return (
    <div className={clsx("min-w-0 rounded-lg border border-border bg-surface-raised/40 p-3.5", className)}>
      <h3 className="text-[11px] font-semibold uppercase leading-4 tracking-[0.1em] text-muted-foreground">
        {title}
      </h3>
      {description ? <p className="mt-0.5 text-[11px] leading-snug text-muted-foreground">{description}</p> : null}
      <div className="mt-2.5">{children}</div>
    </div>
  );
}
