import { useRef } from "react";
import clsx from "clsx";
import { Search, X } from "lucide-react";

// Input is the shared text field chrome. onChange is the native React handler
// (event.target.value), matching plain <input> usage.
export function Input({ className, size = "md", invalid = false, ...rest }) {
  return (
    <input
      className={clsx(
        "w-full rounded-lg border bg-surface px-2.5 text-sm text-foreground transition-colors placeholder:text-muted-foreground/60 disabled:cursor-not-allowed disabled:opacity-50",
        size === "sm" ? "h-7 text-xs" : "h-8",
        invalid
          ? "border-danger/60 focus:border-danger"
          : "border-border hover:border-border-strong focus:border-primary/60",
        className,
      )}
      aria-invalid={invalid || undefined}
      {...rest}
    />
  );
}

export function SearchInput({
  value,
  onChange,
  placeholder = "Search…",
  onClear,
  className,
  inputClassName,
  id,
  ...rest
}) {
  const inputRef = useRef(null);
  const { onKeyDown, "aria-label": ariaLabel, ...inputProps } = rest;

  function clear() {
    onClear?.();
    onChange?.({ target: { value: "" } });
    inputRef.current?.focus();
  }

  return (
    <div className={clsx("group relative min-w-0", className)}>
      <Search
        aria-hidden="true"
        className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
      />
      <input
        {...inputProps}
        ref={inputRef}
        id={id}
        type="text"
        value={value}
        onChange={onChange}
        placeholder={placeholder}
        aria-label={ariaLabel || placeholder}
        onKeyDown={(event) => {
          if (event.key === "Escape" && value) {
            event.preventDefault();
            event.stopPropagation();
            clear();
            return;
          }
          onKeyDown?.(event);
        }}
        className={clsx(
          "h-8 w-full rounded-lg border border-border bg-surface pl-8 pr-8 text-sm text-foreground transition-colors placeholder:text-muted-foreground/60 hover:border-border-strong focus:border-primary/60",
          inputClassName,
        )}
      />
      {value ? (
        <button
          type="button"
          onClick={clear}
          aria-label="Clear search"
          className="absolute right-1 top-1/2 grid h-6 w-6 -translate-y-1/2 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <X aria-hidden="true" className="h-3 w-3" />
        </button>
      ) : null}
    </div>
  );
}
