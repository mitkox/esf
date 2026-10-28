import clsx from "clsx";
import { StatusDot } from "@/components/ui/badge";
import { TONE_BORDER, TONE_SOFT, TONE_TEXT } from "@/components/ui/tones";

const SIZES = {
  xs: "gap-1 px-1.5 py-0 text-[10px] leading-4",
  sm: "gap-1.5 px-2 py-0.5 text-[11px] leading-4",
};

// BoolChip renders a configured boolean without ever turning "missing" into
// "off": undefined/null is an explicit unknown state, which is what the
// governance screens need to stay honest about partial payloads.
export function BoolChip({
  value,
  onLabel = "On",
  offLabel = "Off",
  unknownLabel = "Unknown",
  toneOn = "success",
  toneOff = "muted",
  toneUnknown = "muted",
  size = "sm",
  className,
  title,
}) {
  const known = typeof value === "boolean";
  const on = value === true;
  const tone = known ? (on ? toneOn : toneOff) : toneUnknown;
  const label = known ? (on ? onLabel : offLabel) : unknownLabel;
  return (
    <span
      title={title}
      className={clsx(
        "inline-flex max-w-full items-center whitespace-nowrap rounded-full border font-medium",
        SIZES[size] || SIZES.sm,
        TONE_BORDER[tone] || TONE_BORDER.muted,
        TONE_SOFT[tone] || TONE_SOFT.muted,
        TONE_TEXT[tone] || TONE_TEXT.muted,
        className,
      )}
    >
      <StatusDot tone={tone} size={size === "xs" ? "xs" : "sm"} />
      <span className="truncate">{label}</span>
    </span>
  );
}

// ReachChip renders a probe verdict together with its decided flag. An
// undecided probe is its own state: never "blocked", never "healthy".
export function ReachChip({
  decided,
  reachable,
  reachableLabel = "Reachable",
  blockedLabel = "Blocked",
  undecidedLabel = "Undecided",
  toneReachable = "warning",
  toneBlocked = "success",
  size = "sm",
  className,
  title,
}) {
  if (decided !== true) {
    return (
      <BoolChip
        value={undefined}
        unknownLabel={undecidedLabel}
        size={size}
        toneUnknown="muted"
        className={className}
        title={title || "The probe produced no usable verdict"}
      />
    );
  }
  return (
    <BoolChip
      value={reachable === true}
      onLabel={reachableLabel}
      offLabel={blockedLabel}
      toneOn={toneReachable}
      toneOff={toneBlocked}
      size={size}
      className={className}
      title={title}
    />
  );
}
