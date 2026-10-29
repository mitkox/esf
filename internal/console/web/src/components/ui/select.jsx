import { useId } from "react";
import clsx from "clsx";
import { ChevronDown } from "lucide-react";

// Select wraps a native <select> (full keyboard and platform behaviour) in the
// console's field chrome. onChange receives the native event; read
// event.target.value, exactly like a plain select.
export function Select({
  label,
  value,
  onChange,
  options = [],
  className,
  ariaLabel,
  id,
  size = "sm",
  disabled = false,
  ...rest
}) {
  const generatedID = useId();
  const selectID = id || generatedID;
  return (
    <div className={clsx("min-w-0", className)}>
      {label ? (
        <label htmlFor={selectID} className="mb-1 block text-[11px] font-medium leading-4 text-muted-foreground">
          {label}
        </label>
      ) : null}
      <div className="relative">
        <select
          {...rest}
          id={selectID}
          aria-label={ariaLabel || (label ? undefined : "Select option")}
          value={value}
          disabled={disabled}
          onChange={onChange}
          className={clsx(
            "w-full cursor-pointer appearance-none rounded-lg border border-border bg-surface pl-2.5 pr-7 font-medium text-foreground transition-colors hover:border-border-strong focus:border-primary/60 disabled:cursor-not-allowed disabled:opacity-50",
            size === "md" ? "h-9 text-[13px]" : "h-8 text-xs",
          )}
        >
          {options.map((option) => (
            <option key={option.value} value={option.value} disabled={option.disabled}>
              {option.label}
            </option>
          ))}
        </select>
        <ChevronDown
          aria-hidden="true"
          className="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
        />
      </div>
    </div>
  );
}
