import clsx from "clsx";
import { TONE_BORDER, TONE_CHIP, TONE_DOT, TONE_SOFT, TONE_TEXT } from "./tones";

const DOT_SIZES = {
  xs: "h-1 w-1",
  sm: "h-1.5 w-1.5",
  md: "h-2 w-2",
};

// StatusDot never encodes meaning by colour alone; it is always paired with a
// label in StatusPill / attention menus. Decorative here on purpose.
export function StatusDot({ tone = "muted", size = "sm", pulse = false, className }) {
  return (
    <span
      aria-hidden="true"
      className={clsx(
        "inline-block shrink-0 rounded-full",
        DOT_SIZES[size] || DOT_SIZES.sm,
        TONE_DOT[tone] || TONE_DOT.muted,
        pulse && "animate-pulse",
        className,
      )}
    />
  );
}

const BADGE_VARIANTS = {
  default: "border-border bg-surface-raised text-foreground",
  muted: "border-border bg-muted text-muted-foreground",
  primary: "border-primary/25 bg-primary/10 text-primary",
  success: "border-success/25 bg-success/10 text-success",
  warning: "border-warning/25 bg-warning/10 text-warning",
  danger: "border-danger/25 bg-danger/10 text-danger",
  security: "border-security/25 bg-security/10 text-security",
  info: "border-info/25 bg-info/10 text-info",
  outline: "border-border bg-transparent text-muted-foreground",
};

const BADGE_SIZES = {
  xs: "gap-1 px-1.5 py-0 text-[10px] leading-4",
  sm: "gap-1.5 px-2 py-0.5 text-[11px] leading-4",
  md: "gap-1.5 px-2.5 py-1 text-[11px] leading-4",
};

const BADGE_TONE = {
  default: "muted",
  muted: "muted",
  primary: "primary",
  success: "success",
  warning: "warning",
  danger: "danger",
  security: "security",
  info: "info",
  outline: "muted",
};

export function Badge({ variant = "default", size = "sm", dot = false, className, children, title, ...rest }) {
  const tone = typeof dot === "string" ? dot : BADGE_TONE[variant] || "muted";
  return (
    <span
      title={title}
      className={clsx(
        "inline-flex max-w-full items-center rounded-md border font-medium",
        BADGE_VARIANTS[variant] || BADGE_VARIANTS.default,
        BADGE_SIZES[size] || BADGE_SIZES.sm,
        className,
      )}
      {...rest}
    >
      {dot ? <StatusDot tone={tone} size={size === "xs" ? "xs" : "sm"} /> : null}
      <span className="truncate">{children}</span>
    </span>
  );
}

// Convenience helpers so screens can tint arbitrary text consistently.
export function toneText(tone) {
  return TONE_TEXT[tone] || TONE_TEXT.muted;
}

export function toneSoft(tone) {
  return TONE_SOFT[tone] || TONE_SOFT.muted;
}

export function toneChip(tone) {
  return TONE_CHIP[tone] || TONE_CHIP.muted;
}

export function toneBorder(tone) {
  return TONE_BORDER[tone] || TONE_BORDER.muted;
}
