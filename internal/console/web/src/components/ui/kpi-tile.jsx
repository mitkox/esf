import clsx from "clsx";
import { ArrowUpRight, Minus, TrendingDown, TrendingUp } from "lucide-react";
import { formatDelta } from "@/lib/format";
import { Card } from "./card";
import { KpiSkeleton } from "./skeleton";
import { TONE_SOFT, TONE_TEXT } from "./tones";

function Sparkline({ values, className }) {
  const points = (Array.isArray(values) ? values : [])
    .map((value) => Number(value))
    .filter((value) => Number.isFinite(value));
  if (points.length < 2) return null;
  const width = 100;
  const height = 32;
  const inset = 2;
  const min = Math.min(...points);
  const max = Math.max(...points);
  const range = max - min || 1;
  const coords = points.map((value, index) => {
    const x = inset + (index * (width - inset * 2)) / (points.length - 1);
    const y = height - inset - ((value - min) / range) * (height - inset * 2);
    return [Number(x.toFixed(2)), Number(y.toFixed(2))];
  });
  const line = coords.map(([x, y], index) => `${index === 0 ? "M" : "L"}${x},${y}`).join(" ");
  const area = `${line} L${coords[coords.length - 1][0]},${height} L${coords[0][0]},${height} Z`;
  return (
    <svg
      aria-hidden="true"
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      className={clsx("h-9 w-full", className)}
    >
      <path d={area} fill="currentColor" opacity="0.09" />
      <path d={line} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function normalizeDelta(delta) {
  if (delta === null || delta === undefined || delta === "") return null;
  let raw = delta;
  let forcedTone = "";
  if (typeof delta === "object") {
    raw = delta.value ?? delta.label ?? "";
    forcedTone = delta.tone || "";
  }
  const text = typeof raw === "number" ? formatDelta(raw) : String(raw).trim();
  if (!text) return null;
  const negative = /^[-−]/.test(text);
  const positive = /^\+/.test(text);
  const tone = forcedTone || (negative ? "danger" : positive ? "success" : "muted");
  const Icon = negative ? TrendingDown : positive ? TrendingUp : Minus;
  return { text, tone, Icon };
}

export function KpiTile({
  label,
  value,
  hint,
  delta,
  tone = "muted",
  icon,
  spark,
  footer,
  href,
  loading = false,
  className,
  ...rest
}) {
  if (loading) return <KpiSkeleton className={className} />;

  const deltaInfo = normalizeDelta(delta);
  const accentTone = TONE_TEXT[tone] ? tone : "muted";
  const body = (
    <div className="flex h-full flex-col">
      <div className="flex items-start justify-between gap-3 px-4 pt-3.5">
        <div className="min-w-0">
          <p className="truncate text-[10px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground">
            {label}
          </p>
          <div className="mt-1.5 flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <span className="tabular text-[26px] font-semibold leading-none tracking-[-0.02em] text-foreground">
              {value === null || value === undefined || value === "" ? "—" : value}
            </span>
            {deltaInfo ? (
              <span
                className={clsx(
                  "tabular inline-flex items-center gap-0.5 rounded-md px-1.5 py-0.5 text-[10px] font-semibold leading-4",
                  TONE_SOFT[deltaInfo.tone] || TONE_SOFT.muted,
                  TONE_TEXT[deltaInfo.tone] || TONE_TEXT.muted,
                )}
              >
                <deltaInfo.Icon aria-hidden="true" className="h-3 w-3" />
                {deltaInfo.text}
              </span>
            ) : null}
          </div>
          {hint ? <p className="mt-1.5 line-clamp-2 text-[11px] leading-snug text-muted-foreground">{hint}</p> : null}
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {icon ? (
            <span
              className={clsx(
                "grid h-8 w-8 place-items-center rounded-lg [&>svg]:h-4 [&>svg]:w-4",
                TONE_SOFT[accentTone] || TONE_SOFT.muted,
                TONE_TEXT[accentTone] || TONE_TEXT.muted,
              )}
            >
              {icon}
            </span>
          ) : null}
          {href ? (
            <ArrowUpRight
              aria-hidden="true"
              className="h-3.5 w-3.5 text-muted-foreground/0 transition-colors group-hover:text-muted-foreground"
            />
          ) : null}
        </div>
      </div>
      {spark && spark.length ? (
        <div className={clsx("mt-auto px-1 pt-1", TONE_TEXT[accentTone] || TONE_TEXT.muted)}>
          <Sparkline values={spark} />
        </div>
      ) : null}
      {footer ? (
        <div className="mt-auto flex items-center gap-1.5 border-t border-border px-4 py-2 text-[11px] leading-4 text-muted-foreground">
          {footer}
        </div>
      ) : spark && spark.length ? null : (
        <div className="h-3" />
      )}
    </div>
  );

  return (
    <Card
      className={clsx(
        "group overflow-hidden transition-colors",
        href && "hover:border-border-strong focus-within:border-border-strong",
        className,
      )}
      {...rest}
    >
      {href ? (
        <a href={href} className="block h-full rounded-panel focus-visible:outline-offset-4">
          {body}
        </a>
      ) : (
        body
      )}
    </Card>
  );
}

export { Sparkline };
