import clsx from "clsx";
import { CopyButton } from "@/components/ui/copy-button";

// DefinitionList is the dense key/value surface used by the change record
// panel. Mono values keep identifiers scannable and copyable.
export function DefinitionList({ rows = [], className }) {
  return (
    <dl className={clsx("divide-y divide-border/70", className)}>
      {rows.map((row) => (
        <div key={row.key || row.label} className="flex min-w-0 items-start gap-3 py-1.5">
          <dt className="w-[38%] shrink-0 pt-0.5 text-[10.5px] font-medium uppercase leading-4 tracking-[0.06em] text-muted-foreground">
            {row.label}
          </dt>
          <dd
            className={clsx("flex min-w-0 flex-1 items-center justify-end gap-1 text-right text-[11.5px] leading-5", row.valueClassName)}
            title={row.title || undefined}
          >
            <span className={clsx("min-w-0 truncate", row.mono && "mono")}>{row.value}</span>
            {row.copy ? <CopyButton value={row.copy} label={row.label} size="xs" className="shrink-0" /> : null}
          </dd>
        </div>
      ))}
    </dl>
  );
}
