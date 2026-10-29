import { useMemo, useState } from "react";
import clsx from "clsx";
import { formatNumber, formatPercent, runStateLabel, runStateTone } from "@/lib/format";

const TONE_COLOR = {
  success: "var(--success)",
  warning: "var(--warning)",
  danger: "var(--danger)",
  security: "var(--security)",
  info: "var(--info)",
  primary: "var(--primary)",
  muted: "var(--muted-foreground)",
};

const SIZE = 168;
const CENTER = SIZE / 2;
const RADIUS = 62;
const STROKE = 15;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;

// OutcomeDonut renders the factory-result mix as a theme aware SVG ring plus a
// legend that carries the exact counts. Hovering a segment (or its legend row)
// swaps the centre label for that outcome's share; no data is encoded by colour
// alone.
export function OutcomeDonut({ outcomes = [], total, className }) {
  const [activeIndex, setActiveIndex] = useState(null);

  const slices = useMemo(() => {
    const sum =
      Number.isFinite(Number(total)) && Number(total) > 0
        ? Number(total)
        : outcomes.reduce((accumulator, outcome) => accumulator + (Number(outcome.count) || 0), 0);
    let offset = 0;
    return outcomes.map((outcome) => {
      const count = Number(outcome.count) || 0;
      const share = sum > 0 ? count / sum : 0;
      const length = share * CIRCUMFERENCE;
      const slice = {
        value: outcome.value,
        count,
        share,
        length,
        offset,
        color: TONE_COLOR[runStateTone(outcome.value)] || TONE_COLOR.muted,
      };
      offset += length;
      return slice;
    });
  }, [outcomes, total]);

  const sum = slices.reduce((accumulator, slice) => accumulator + slice.count, 0);
  const active = activeIndex !== null ? slices[activeIndex] : null;
  const ariaLabel = useMemo(() => {
    if (!slices.length) return "Outcome mix: no results in this window.";
    const parts = slices.map(
      (slice) => `${runStateLabel(slice.value)} ${formatNumber(slice.count)} runs (${formatPercent(slice.share)})`,
    );
    return `Outcome mix across ${formatNumber(sum)} runs: ${parts.join("; ")}.`;
  }, [slices, sum]);

  return (
    <div className={clsx("flex flex-wrap items-center justify-center gap-x-5 gap-y-4", className)}>
      <div
        className="relative h-[168px] w-[168px] shrink-0"
        role="img"
        aria-label={ariaLabel}
        onMouseLeave={() => setActiveIndex(null)}
      >
        <svg viewBox={`0 0 ${SIZE} ${SIZE}`} className="h-full w-full" aria-hidden="true">
          <circle cx={CENTER} cy={CENTER} r={RADIUS} fill="none" stroke="var(--muted)" strokeWidth={STROKE} />
          <g transform={`rotate(-90 ${CENTER} ${CENTER})`}>
            {slices.map((slice, index) => {
              const gap = slice.length > 3 ? 1.2 : 0;
              const drawn = Math.max(0, slice.length - gap);
              const isActive = activeIndex === index;
              return (
                <circle
                  key={slice.value}
                  cx={CENTER}
                  cy={CENTER}
                  r={RADIUS}
                  fill="none"
                  stroke={slice.color}
                  strokeWidth={isActive ? STROKE + 3 : STROKE}
                  strokeDasharray={`${drawn} ${CIRCUMFERENCE - drawn}`}
                  strokeDashoffset={-slice.offset}
                  opacity={activeIndex === null || isActive ? 1 : 0.32}
                  className="cursor-pointer transition-[stroke-width,opacity] duration-150"
                  onMouseEnter={() => setActiveIndex(index)}
                />
              );
            })}
          </g>
        </svg>
        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center px-4 text-center">
          {active ? (
            <>
              <span className="tabular text-xl font-semibold leading-tight text-foreground">
                {formatPercent(active.share)}
              </span>
              <span
                className="mt-0.5 max-w-[7.25rem] truncate text-[10px] font-medium leading-4 text-foreground"
                title={runStateLabel(active.value)}
              >
                {runStateLabel(active.value)}
              </span>
              <span className="tabular text-[10px] leading-4 text-muted-foreground">
                {formatNumber(active.count)} runs
              </span>
            </>
          ) : (
            <>
              <span className="tabular text-[24px] font-semibold leading-none text-foreground">
                {formatNumber(sum)}
              </span>
              <span className="mt-1 text-[9px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                Runs
              </span>
            </>
          )}
        </div>
      </div>

      <ul className="min-w-[13rem] flex-1 space-y-0.5" aria-label="Outcome breakdown">
        {slices.map((slice, index) => (
          <li key={slice.value}>
            <div
              className={clsx(
                "flex items-center gap-2 rounded px-1 py-[3px] text-[11px] leading-4 transition-colors",
                activeIndex === index ? "bg-muted/70" : null,
              )}
              onMouseEnter={() => setActiveIndex(index)}
              onMouseLeave={() => setActiveIndex(null)}
            >
              <span
                aria-hidden="true"
                className="h-2 w-2 shrink-0 rounded-[2px]"
                style={{ backgroundColor: slice.color }}
              />
              <span className="min-w-0 flex-1 truncate text-foreground" title={runStateLabel(slice.value)}>
                {runStateLabel(slice.value)}
              </span>
              <span className="tabular shrink-0 text-muted-foreground">{formatNumber(slice.count)}</span>
              <span className="tabular w-10 shrink-0 text-right text-muted-foreground">
                {formatPercent(slice.share)}
              </span>
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
