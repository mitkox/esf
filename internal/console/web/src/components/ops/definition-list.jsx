import clsx from "clsx";
import { BoolChip } from "./chips";

const COLUMNS = {
  1: "",
  2: "sm:grid-cols-2",
  3: "sm:grid-cols-2 xl:grid-cols-3",
  4: "sm:grid-cols-2 xl:grid-cols-4",
};

function Value({ value, mono }) {
  if (typeof value === "boolean") return <BoolChip value={value} size="xs" />;
  if (value === null || value === undefined || value === "") {
    return <span className="text-muted-foreground">—</span>;
  }
  if (mono) return <span className="mono break-all">{value}</span>;
  return value;
}

// DefinitionList is the dense label/value grid used by every configuration
// panel. It accepts nodes (chips, links, formatted numbers) as values and
// never claims a value that the payload did not provide.
export function DefinitionList({ items = [], columns = 2, className }) {
  return (
    <dl className={clsx("grid gap-x-6 gap-y-3.5", COLUMNS[columns] || COLUMNS[2], className)}>
      {items.filter(Boolean).map((item, index) => {
        const spanClass =
          item.span === "full" ? "sm:col-span-full" : item.span === 2 ? "sm:col-span-2" : undefined;
        return (
          <div key={`${item.label}:${index}`} className={clsx("min-w-0", spanClass)}>
            <dt className="text-[10px] font-semibold uppercase leading-4 tracking-[0.1em] text-muted-foreground">
              {item.label}
            </dt>
            <dd className={clsx("mt-0.5 min-w-0 text-[13px] leading-relaxed text-foreground", item.valueClassName)}>
              <Value value={item.value} mono={item.mono} />
            </dd>
            {item.hint ? <p className="mt-0.5 text-[11px] leading-snug text-muted-foreground">{item.hint}</p> : null}
          </div>
        );
      })}
    </dl>
  );
}
