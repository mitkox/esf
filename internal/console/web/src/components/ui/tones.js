// Shared semantic tone maps.
//
// Every primitive that renders a status speaks the same colour language so a
// warning stays amber across badges, pills, KPI tiles and menus. The class
// strings are complete literals on purpose: Tailwind's scanner must see them.

export const TONE_TEXT = {
  success: "text-success",
  warning: "text-warning",
  danger: "text-danger",
  security: "text-security",
  info: "text-info",
  primary: "text-primary",
  accent: "text-accent",
  muted: "text-muted-foreground",
};

export const TONE_SOFT = {
  success: "bg-success/10",
  warning: "bg-warning/10",
  danger: "bg-danger/10",
  security: "bg-security/10",
  info: "bg-info/10",
  primary: "bg-primary/10",
  accent: "bg-accent/10",
  muted: "bg-muted",
};

export const TONE_BORDER = {
  success: "border-success/25",
  warning: "border-warning/25",
  danger: "border-danger/25",
  security: "border-security/25",
  info: "border-info/25",
  primary: "border-primary/25",
  accent: "border-accent/25",
  muted: "border-border",
};

export const TONE_DOT = {
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
  security: "bg-security",
  info: "bg-info",
  primary: "bg-primary",
  accent: "bg-accent",
  muted: "bg-muted-foreground/50",
};

export const TONE_CHIP = {
  success: "border border-success/25 bg-success/10 text-success",
  warning: "border border-warning/25 bg-warning/10 text-warning",
  danger: "border border-danger/25 bg-danger/10 text-danger",
  security: "border border-security/25 bg-security/10 text-security",
  info: "border border-info/25 bg-info/10 text-info",
  primary: "border border-primary/25 bg-primary/10 text-primary",
  accent: "border border-accent/25 bg-accent/10 text-accent",
  muted: "border border-border bg-muted text-muted-foreground",
};

export function toneClass(map, tone, fallback = "muted") {
  return map[tone] || map[fallback] || map.muted;
}
