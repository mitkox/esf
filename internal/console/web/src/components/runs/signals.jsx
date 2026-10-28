import clsx from "clsx";
import { ShieldAlert, ShieldCheck, ShieldOff } from "lucide-react";
import { Badge, StatusDot } from "@/components/ui/badge";
import { TONE_TEXT } from "@/components/ui/tones";
import { cleanupMeta, outcomeMeta, reviewMeta, securityMeta } from "./utils";

// Status signalling shared by the runs table and the run detail header. Every
// indicator pairs a dot or icon with a text label so colour is never the only
// carrier of meaning.

export function OutcomeBadge({ value, size = "sm", className }) {
  const meta = outcomeMeta(value);
  return (
    <Badge
      variant={meta.tone === "muted" ? "muted" : meta.tone}
      size={size}
      dot
      className={className}
      title={value ? `Recorded outcome: ${value}` : "No outcome recorded"}
    >
      {meta.label}
    </Badge>
  );
}

export function ResultLine({ label, value }) {
  const meta = outcomeMeta(value);
  return (
    <div className="flex items-center gap-1.5 text-[11px] leading-4">
      <StatusDot tone={meta.tone} size="xs" />
      <span className="w-[3.25rem] shrink-0 text-muted-foreground">{label}</span>
      <span className={clsx("truncate", TONE_TEXT[meta.tone] || TONE_TEXT.muted)}>{meta.label}</span>
    </div>
  );
}

export function ResultSplit({ run }) {
  return (
    <div className="space-y-0.5">
      <ResultLine label="Agent" value={run?.agent_result} />
      <ResultLine label="Verify" value={run?.verification_result} />
    </div>
  );
}

export function CleanupIndicator({ cleanup, className }) {
  const meta = cleanupMeta(cleanup);
  return (
    <span className={clsx("inline-flex items-center gap-1.5 text-[11px]", className)} title={meta.detail}>
      <StatusDot tone={meta.tone} size="xs" />
      <span className={TONE_TEXT[meta.tone] || TONE_TEXT.muted}>{meta.label}</span>
    </span>
  );
}

export function SecurityIndicator({ hardening, state, className }) {
  const meta = securityMeta(hardening, state);
  const Icon = meta.tone === "muted" ? ShieldOff : meta.tone === "success" ? ShieldCheck : ShieldAlert;
  return (
    <span className={clsx("inline-flex items-center gap-1.5 text-[11px]", className)} title={`${meta.label} — ${meta.detail}`}>
      <Icon aria-hidden="true" className={clsx("h-3.5 w-3.5 shrink-0", TONE_TEXT[meta.tone] || TONE_TEXT.muted)} />
      <span className={TONE_TEXT[meta.tone] || TONE_TEXT.muted}>{meta.label}</span>
    </span>
  );
}

export function ReviewIndicator({ run, size = "xs", className }) {
  const meta = reviewMeta(run);
  return (
    <Badge
      variant={meta.tone === "muted" ? "muted" : meta.tone}
      size={size}
      dot
      className={className}
      title={meta.detail}
    >
      {meta.label}
    </Badge>
  );
}
