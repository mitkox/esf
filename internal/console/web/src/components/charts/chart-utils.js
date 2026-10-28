// Shared geometry helpers for the hand-rolled SVG charts.
//
// The console has no charting dependency: every chart is an SVG rendered from
// the real API payload. These helpers keep the tick maths and the bar path
// builders identical across the overview panels.

export function clamp(value, min, max) {
  if (!Number.isFinite(value)) return min;
  return Math.min(max, Math.max(min, value));
}

// niceStep rounds a raw interval up to a 1/2/5 * 10^n step so axis labels stay
// readable regardless of the payload's magnitude.
export function niceStep(raw) {
  const safe = Number.isFinite(raw) && raw > 0 ? raw : 1;
  const power = Math.pow(10, Math.floor(Math.log10(safe)));
  const normalized = safe / power;
  const factor = normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10;
  return factor * power;
}

// axisTicks returns the grid values for a 0-based count axis. Counts are
// integers, so the step is clamped to at least 1 — otherwise a smaller axis
// would round two ticks onto the same value.
export function axisTicks(maxValue, count = 4) {
  const safeMax = Math.max(1, Math.ceil(Number(maxValue) || 0));
  const step = Math.max(1, Math.round(niceStep(safeMax / Math.max(1, count))));
  const top = Math.ceil(safeMax / step) * step;
  const ticks = [];
  for (let value = 0; value <= top; value += step) {
    ticks.push(value);
  }
  return { ticks, max: top, step };
}

export function formatAxisCount(value) {
  const number = Number(value) || 0;
  if (Math.abs(number) >= 1000000) {
    const scaled = number / 1000000;
    return `${Number.isInteger(scaled) ? scaled : scaled.toFixed(1)}m`;
  }
  if (Math.abs(number) >= 1000) {
    const scaled = number / 1000;
    return `${Number.isInteger(scaled) ? scaled : scaled.toFixed(1)}k`;
  }
  return String(Math.round(number));
}

// topRoundedBar produces a bar path with only the top corners rounded, so a
// stacked column reads as one shape while each segment keeps a crisp base.
export function topRoundedBar(x, y, width, height, radius = 0) {
  const w = Math.max(0.5, Number(width) || 0);
  const h = Math.max(0, Number(height) || 0);
  if (h <= 0.5) return "";
  const r = Math.max(0, Math.min(radius, w / 2, h));
  if (r === 0) {
    return `M${x},${y}h${w}v${h}h${-w}Z`;
  }
  return `M${x},${y + h}V${y + r}a${r},${r} 0 0 1 ${r},${-r}h${w - 2 * r}a${r},${r} 0 0 1 ${r},${r}V${y + h}Z`;
}

// xLabelStep limits the number of x-axis labels to what the plot can hold.
export function xLabelStep(count, plotWidth, minSpacing = 62) {
  const capacity = Math.max(2, Math.floor(plotWidth / Math.max(24, minSpacing)));
  return Math.max(1, Math.ceil(count / capacity));
}
