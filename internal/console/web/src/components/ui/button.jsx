import clsx from "clsx";
import { Spinner } from "./spinner";

const VARIANTS = {
  primary:
    "border-primary bg-primary text-primary-foreground shadow-[inset_0_1px_0_rgb(255_255_255/0.12)] hover:bg-[color-mix(in_srgb,var(--primary)_88%,var(--foreground))] active:bg-[color-mix(in_srgb,var(--primary)_82%,var(--foreground))]",
  secondary: "border-border bg-surface text-foreground hover:bg-muted active:bg-muted/80",
  outline: "border-border-strong bg-transparent text-foreground hover:bg-muted/70 active:bg-muted",
  ghost: "border-transparent bg-transparent text-muted-foreground hover:bg-muted hover:text-foreground",
  subtle: "border-transparent bg-muted text-foreground hover:bg-muted/75",
  danger: "border-danger/30 bg-danger/10 text-danger hover:bg-danger/15 active:bg-danger/20",
  link: "border-transparent bg-transparent px-0 text-primary hover:underline underline-offset-4",
};

const SIZES = {
  xs: "h-6 gap-1 rounded-md px-1.5 text-[11px] font-medium",
  sm: "h-7 gap-1.5 rounded-lg px-2.5 text-xs font-medium",
  md: "h-8 gap-1.5 rounded-lg px-3 text-xs font-semibold",
  lg: "h-9 gap-2 rounded-lg px-3.5 text-sm font-semibold",
  "icon-xs": "h-6 w-6 rounded-md p-0",
  "icon-sm": "h-7 w-7 rounded-lg p-0",
  icon: "h-8 w-8 rounded-lg p-0",
  "icon-lg": "h-9 w-9 rounded-lg p-0",
};

const BASE =
  "inline-flex shrink-0 select-none items-center justify-center whitespace-nowrap border font-sans transition-colors duration-100 disabled:pointer-events-none disabled:opacity-45";

// buttonStyles is exported because anchor/links and third-party triggers need
// the exact same visual contract as <Button> without the wrapper element.
export function buttonStyles({ variant = "secondary", size = "sm", className } = {}) {
  return clsx(BASE, VARIANTS[variant] || VARIANTS.secondary, SIZES[size] || SIZES.sm, className);
}

export function Button({
  variant = "secondary",
  size = "sm",
  className,
  children,
  icon,
  iconRight,
  loading = false,
  disabled = false,
  type = "button",
  as,
  href,
  ...rest
}) {
  const Tag = as || (href ? "a" : "button");
  const content = (
    <>
      {loading ? <Spinner size={size === "lg" ? "md" : "sm"} /> : icon ? <span className="shrink-0 [&>svg]:h-3.5 [&>svg]:w-3.5">{icon}</span> : null}
      {children ? <span className="truncate">{children}</span> : null}
      {iconRight ? <span className="shrink-0 [&>svg]:h-3.5 [&>svg]:w-3.5">{iconRight}</span> : null}
    </>
  );
  const props = {
    className: buttonStyles({ variant, size, className }),
    "aria-busy": loading || undefined,
    ...rest,
  };
  if (Tag === "a") {
    return (
      <a href={href} aria-disabled={disabled || loading || undefined} {...props}>
        {content}
      </a>
    );
  }
  return (
    <Tag type={type} disabled={disabled || loading} {...props}>
      {content}
    </Tag>
  );
}
