import { useId, useMemo, useState } from "react";
import clsx from "clsx";
import { formatDuration, formatNumber, formatUsd } from "@/lib/format";
import { axisTicks, clamp, formatAxisCount, topRoundedBar, xLabelStep } from "./chart-utils";
import { useElementWidth } from "./use-element-width";

// The throughput series is fixed by the API contract: every bucket carries
// succeeded / failed / security / other counts. Colours are semantic and come
// from the design tokens so light and dark themes stay consistent.
export const THROUGHPUT_SERIES = [
  { key: "succeeded", label: "Succeeded", color: "var(--success)" },
  { key: "failed", label: "Failed", color: "var(--danger)" },
  { key: "security", label: "Security", color: "var(--security)" },
  { key: "other", label: "Other", color: "var(--muted-foreground)", opacity: 0.75 },
];

const PADDING = { top: 12, right: 6, bottom: 22, left: 34 };
const DEFAULT_HEIGHT = 232;

const UNIT_LABEL = { hour: "hour", day: "day", month: "month" };

export function throughputTotals(points = []) {
  const totals = { total: 0 };
  for (const series of THROUGHPUT_SERIES) totals[series.key] = 0;
  for (const point of points) {
    totals.total += Number(point.total) || 0;
    for (const series of THROUGHPUT_SERIES) {
      totals[series.key] += Number(point[series.key]) || 0;
    }
  }
  return totals;
}

export function StackedChartLegend({ points = [], className }) {
  const totals = throughputTotals(points);
  return (
    <div className={clsx("flex flex-wrap items-center gap-x-4 gap-y-1.5", className)}>
      {THROUGHPUT_SERIES.map((series) => (
        <span key={series.key} className="inline-flex items-center gap-1.5 text-[11px] leading-4 text-muted-foreground">
          <span
            aria-hidden="true"
            className="h-2 w-2 shrink-0 rounded-[2px]"
            style={{ backgroundColor: series.color, opacity: series.opacity || 1 }}
          />
          <span>{series.label}</span>
          <span className="tabular font-medium text-foreground">{formatNumber(totals[series.key])}</span>
        </span>
      ))}
    </div>
  );
}

// StackedBarChart renders one stacked column per series bucket. Hover (or
// arrow keys when focused) reveals the exact breakdown of a bucket; the
// accessible summary lives on the focusable wrapper and a live region announces
// the selected bucket so the chart is usable without a pointer.
export function StackedBarChart({
  points = [],
  granularity = "day",
  height = DEFAULT_HEIGHT,
  className,
  ariaLabel,
  emptyLabel = "No activity",
}) {
  const [wrapperRef, width] = useElementWidth();
  const [activeIndex, setActiveIndex] = useState(null);
  const assistiveId = useId();
  const count = points.length;

  const layout = useMemo(() => {
    if (!count || width <= 0) return null;
    const plotWidth = Math.max(1, width - PADDING.left - PADDING.right);
    const plotHeight = Math.max(1, height - PADDING.top - PADDING.bottom);
    const maxTotal = Math.max(1, ...points.map((point) => Number(point.total) || 0));
    const scale = axisTicks(maxTotal, 4);
    const band = plotWidth / count;
    const barWidth = clamp(band * 0.62, 2.5, 34);
    const baseline = PADDING.top + plotHeight;
    const xFor = (index) => PADDING.left + band * index + (band - barWidth) / 2;
    const yFor = (value) => baseline - ((Number(value) || 0) / scale.max) * plotHeight;
    const labelEvery = xLabelStep(count, plotWidth);
    return { plotWidth, plotHeight, band, barWidth, baseline, scale, xFor, yFor, labelEvery };
  }, [count, width, height, points]);

  const activePoint =
    activeIndex !== null && activeIndex >= 0 && activeIndex < count ? points[activeIndex] : null;

  const announce = useMemo(() => {
    if (!activePoint) return "";
    const parts = THROUGHPUT_SERIES.filter((series) => Number(activePoint[series.key]) > 0).map(
      (series) => `${series.label} ${formatNumber(activePoint[series.key])}`,
    );
    return `${activePoint.label}: ${formatNumber(activePoint.total || 0)} runs${parts.length ? `; ${parts.join(", ")}` : "; no runs"}.`;
  }, [activePoint]);

  function indexFromEvent(event) {
    if (!layout) return null;
    const rect = event.currentTarget.getBoundingClientRect();
    if (!rect.width) return null;
    const offset = event.clientX - rect.left - PADDING.left;
    return clamp(Math.floor(offset / layout.band), 0, count - 1);
  }

  function handlePointerMove(event) {
    const index = indexFromEvent(event);
    if (index !== null) setActiveIndex(index);
  }

  function handleKeyDown(event) {
    if (!count) return;
    const current = activeIndex === null ? count - 1 : activeIndex;
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      setActiveIndex(Math.max(0, current - 1));
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      setActiveIndex(Math.min(count - 1, current + 1));
    } else if (event.key === "Home") {
      event.preventDefault();
      setActiveIndex(0);
    } else if (event.key === "End") {
      event.preventDefault();
      setActiveIndex(count - 1);
    } else if (event.key === "Escape") {
      setActiveIndex(null);
    }
  }

  const tooltipLeft =
    layout && activeIndex !== null
      ? clamp(PADDING.left + layout.band * (activeIndex + 0.5), 100, Math.max(100, width - 100))
      : 0;

  return (
    <div
      ref={wrapperRef}
      className={clsx("relative select-none rounded-md", count ? "cursor-crosshair" : null, className)}
      tabIndex={count ? 0 : -1}
      role="group"
      aria-label={ariaLabel || `Stacked throughput chart by ${UNIT_LABEL[granularity] || "bucket"}`}
      aria-describedby={assistiveId}
      onPointerMove={handlePointerMove}
      onPointerLeave={() => setActiveIndex(null)}
      onFocus={() => setActiveIndex((current) => (current === null ? Math.max(0, count - 1) : current))}
      onBlur={() => setActiveIndex(null)}
      onKeyDown={handleKeyDown}
    >
      {layout ? (
        <svg width={width} height={height} aria-hidden="true" className="block max-w-full">
          {layout.scale.ticks.map((tick) => {
            const y = layout.yFor(tick);
            return (
              <g key={`tick-${tick}`}>
                <line
                  x1={PADDING.left}
                  x2={width - PADDING.right}
                  y1={y}
                  y2={y}
                  stroke="var(--border)"
                  strokeOpacity={tick === 0 ? 0.95 : 0.55}
                  shapeRendering="crispEdges"
                />
                <text
                  x={PADDING.left - 8}
                  y={y + 3}
                  textAnchor="end"
                  fontSize="10"
                  fill="var(--muted-foreground)"
                  style={{ fontVariantNumeric: "tabular-nums" }}
                >
                  {formatAxisCount(tick)}
                </text>
              </g>
            );
          })}

          {activeIndex !== null ? (
            <rect
              x={PADDING.left + layout.band * activeIndex}
              y={PADDING.top}
              width={layout.band}
              height={layout.plotHeight}
              fill="var(--muted)"
              opacity="0.5"
              rx="2"
            />
          ) : null}

          {points.map((point, index) => {
            const x = layout.xFor(index);
            const total = Number(point.total) || 0;
            if (total === 0) {
              return (
                <line
                  key={`quiet-${point.date || index}`}
                  x1={x}
                  x2={x + layout.barWidth}
                  y1={layout.baseline}
                  y2={layout.baseline}
                  stroke="var(--border-strong)"
                  strokeWidth="2"
                  strokeLinecap="round"
                />
              );
            }
            let stacked = 0;
            const segments = [];
            for (const series of THROUGHPUT_SERIES) {
              const value = Number(point[series.key]) || 0;
              if (value <= 0) continue;
              const top = layout.yFor(stacked + value);
              const bottom = layout.yFor(stacked);
              segments.push({
                key: series.key,
                y: top,
                height: Math.max(0.75, bottom - top),
                color: series.color,
                opacity: series.opacity || 1,
              });
              stacked += value;
            }
            return (
              <g key={`bar-${point.date || index}`}>
                {segments.map((segment, segmentIndex) => (
                  <path
                    key={`${point.date || index}-${segment.key}`}
                    d={topRoundedBar(
                      x,
                      segment.y,
                      layout.barWidth,
                      segment.height,
                      segmentIndex === segments.length - 1 ? 2 : 0,
                    )}
                    fill={segment.color}
                    opacity={segment.opacity}
                  />
                ))}
              </g>
            );
          })}

          {points.map((point, index) => {
            const show =
              index % layout.labelEvery === 0 ||
              (index === count - 1 && (count - 1) % layout.labelEvery > layout.labelEvery * 0.55);
            if (!show) return null;
            const x = layout.xFor(index) + layout.barWidth / 2;
            return (
              <text
                key={`xlabel-${point.date || index}`}
                x={x}
                y={height - 6}
                textAnchor="middle"
                fontSize="10"
                fill="var(--muted-foreground)"
              >
                {point.label || point.date}
              </text>
            );
          })}
        </svg>
      ) : (
        <div style={{ height }} aria-hidden="true" />
      )}

      {activePoint ? (
        <div
          className="pointer-events-none absolute top-1 z-20 w-44 -translate-x-1/2 rounded-lg border border-border-strong bg-surface-overlay px-2.5 py-2 shadow-popover"
          style={{ left: tooltipLeft }}
          aria-hidden="true"
        >
          <div className="flex items-baseline justify-between gap-2 border-b border-border pb-1">
            <p className="truncate text-[11px] font-semibold leading-4 text-foreground">{activePoint.label}</p>
            <p className="tabular shrink-0 text-[10px] leading-4 text-muted-foreground">
              {formatNumber(activePoint.total || 0)} runs
            </p>
          </div>
          <ul className="mt-1 space-y-0.5">
            {THROUGHPUT_SERIES.map((series) => {
              const value = Number(activePoint[series.key]) || 0;
              return (
                <li key={series.key} className="flex items-center justify-between gap-2 text-[10px] leading-4">
                  <span className="inline-flex min-w-0 items-center gap-1.5 text-muted-foreground">
                    <span
                      aria-hidden="true"
                      className="h-1.5 w-1.5 shrink-0 rounded-[1px]"
                      style={{ backgroundColor: series.color, opacity: series.opacity || 1 }}
                    />
                    <span className="truncate">{series.label}</span>
                  </span>
                  <span
                    className={clsx(
                      "tabular shrink-0",
                      value > 0 ? "font-medium text-foreground" : "text-muted-foreground/60",
                    )}
                  >
                    {formatNumber(value)}
                  </span>
                </li>
              );
            })}
          </ul>
          {Number(activePoint.p50_duration_ms) > 0 || Number(activePoint.cost_usd) > 0 ? (
            <div className="mt-1 space-y-0.5 border-t border-border pt-1 text-[10px] leading-4 text-muted-foreground">
              {Number(activePoint.p50_duration_ms) > 0 ? (
                <p className="flex items-center justify-between gap-2">
                  <span>p50 duration</span>
                  <span className="tabular text-foreground">{formatDuration(activePoint.p50_duration_ms)}</span>
                </p>
              ) : null}
              {Number(activePoint.cost_usd) > 0 ? (
                <p className="flex items-center justify-between gap-2">
                  <span>Reported cost</span>
                  <span className="tabular text-foreground">{formatUsd(activePoint.cost_usd)}</span>
                </p>
              ) : null}
            </div>
          ) : null}
        </div>
      ) : null}

      <p id={assistiveId} className="sr-only" aria-live="polite">
        {activePoint ? announce : emptyLabel}
      </p>
    </div>
  );
}
