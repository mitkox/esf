import clsx from "clsx";
import { BoolChip } from "./chips";
import { asArray } from "./data";
import { TD, TH, THead, TR, Table, TableEmpty } from "@/components/ui/table";

function DefaultValue({ value }) {
  if (typeof value === "boolean") return <BoolChip value={value} size="xs" />;
  if (value === null || value === undefined || value === "") {
    return <span className="text-muted-foreground">—</span>;
  }
  if (Array.isArray(value)) {
    return value.length ? <span className="break-words">{value.join(", ")}</span> : <span className="text-muted-foreground">—</span>;
  }
  return <span className="break-words">{String(value)}</span>;
}

// ConfigTable is the generic read-only table behind the settings catalog:
// callers describe columns and provide render functions for anything richer
// than plain text, and every empty collection gets a real empty state.
export function ConfigTable({
  rows = [],
  columns = [],
  rowKey,
  minWidth,
  className,
  ariaLabel,
  emptyIcon,
  emptyTitle = "Nothing configured",
  emptyDescription,
  emptyAction,
}) {
  const items = asArray(rows);
  return (
    <Table className={className} minWidth={minWidth} aria-label={ariaLabel}>
      <THead>
        <TR>
          {columns.map((column) => (
            <TH
              key={column.key}
              numeric={column.numeric}
              align={column.align}
              className={column.headerClassName}
            >
              {column.label}
            </TH>
          ))}
        </TR>
      </THead>
      <tbody>
        {items.length ? (
          items.map((row, index) => (
            <TR key={rowKey ? rowKey(row, index) : (row?.name ?? row?.id ?? index)}>
              {columns.map((column) => (
                <TD
                  key={column.key}
                  numeric={column.numeric}
                  muted={column.muted}
                  className={clsx(column.numeric && "tabular", column.className)}
                >
                  {column.render ? (
                    column.render(row, index)
                  ) : column.mono ? (
                    <span className="mono break-all">
                      <DefaultValue value={row?.[column.key]} />
                    </span>
                  ) : (
                    <DefaultValue value={row?.[column.key]} />
                  )}
                </TD>
              ))}
            </TR>
          ))
        ) : (
          <TableEmpty
            colSpan={columns.length}
            icon={emptyIcon}
            title={emptyTitle}
            description={emptyDescription}
            action={emptyAction}
          />
        )}
      </tbody>
    </Table>
  );
}
