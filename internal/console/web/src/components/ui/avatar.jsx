import clsx from "clsx";
import { initials } from "@/lib/format";

const SIZES = {
  xs: "h-5 w-5 text-[9px]",
  sm: "h-6 w-6 text-[10px]",
  md: "h-8 w-8 text-[11px]",
  lg: "h-10 w-10 text-[13px]",
};

// Restrained deterministic tints keep avatars distinguishable without turning
// the operator chip into a colour wheel.
const TONES = [
  "border-primary/25 bg-primary/10 text-primary",
  "border-info/25 bg-info/10 text-info",
  "border-security/25 bg-security/10 text-security",
  "border-success/25 bg-success/10 text-success",
  "border-warning/25 bg-warning/10 text-warning",
];

function toneIndex(name) {
  let hash = 0;
  for (const character of String(name || "")) {
    hash = (hash * 31 + character.codePointAt(0)) >>> 0;
  }
  return hash % TONES.length;
}

export function Avatar({ name, size = "md", className, title }) {
  const label = name || "Operator";
  return (
    <span
      role="img"
      aria-label={label}
      title={title || label}
      className={clsx(
        "inline-flex shrink-0 select-none items-center justify-center rounded-full border font-semibold uppercase tracking-tight",
        SIZES[size] || SIZES.md,
        TONES[toneIndex(label)],
        className,
      )}
    >
      {initials(label)}
    </span>
  );
}
