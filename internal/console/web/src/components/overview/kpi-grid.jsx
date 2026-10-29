import {
  ChevronDown,
  SlidersHorizontal,
  CircleCheck,
  CircleDollarSign,
  ClipboardCheck,
  Gauge,
  Hash,
  Hourglass,
  PackageCheck,
  Play,
  Radio,
  ShieldAlert,
  Timer,
} from "lucide-react";
import { KpiTile } from "@/components/ui/kpi-tile";
import { formatDelta, formatDuration, formatNumber, formatPercent, formatUsd, truncate } from "@/lib/format";

// The factory answers four questions before any detail: how much work ran, how
// much of it succeeded, what is still open, and what needs a human. Those four
// lead the grid; the supporting metrics follow in the same visual rhythm.
const KPI_LABELS = [
  "Runs started",
  "Success rate",
  "In flight",
  "Reviews pending",
  "Security stops",
  "Cleanup verified",
  "p50 duration",
  "p95 duration",
  "Tokens",
  "Cost",
];

function Unavailable({ label = "Not reported" }) {
  return <span className="text-sm font-medium leading-none text-muted-foreground">{label}</span>;
}

function deltaArg(kpi, { duration = false, security = false, neutral = false } = {}) {
  if (!kpi || kpi.available === false) return null;
  const raw = kpi.delta_pct;
  if (raw === null || raw === undefined || !Number.isFinite(Number(raw))) return null;
  const value = Number(raw);
  const text = formatDelta(value, { suffix: "%" });
  if (!text) return null;
  if (neutral) return { value: text, tone: "muted" };
  if (security) return { value: text, tone: value > 0 ? "danger" : "success" };
  if (duration) return { value: text, tone: value > 0 ? "warning" : "success" };
  return text;
}

function comparisonHint(kpi, sampleLabel) {
  if (!kpi || kpi.available === false) return truncate(kpi?.note || "Not reported by the factory API.", 96);
  if (kpi.delta_pct === null || kpi.delta_pct === undefined) {
    return sampleLabel ? `No comparable previous window · ${sampleLabel}` : "No comparable previous window";
  }
  return `Previous window: ${formatNumber(kpi.previous)}`;
}

function percentValue(kpi) {
  if (!kpi || kpi.available === false) return <Unavailable label="Unavailable" />;
  return formatPercent(kpi.value);
}

function durationValue(kpi) {
  if (!kpi || kpi.available === false) return <Unavailable label="Unavailable" />;
  return formatDuration(kpi.value);
}

function sparkValues(points, mapper) {
  const values = [];
  for (const point of points) {
    const value = mapper(point);
    if (Number.isFinite(value)) values.push(value);
  }
  return values;
}

export function KpiGrid({ data }) {
  const kpis = data?.kpis || {};
  const points = Array.isArray(data?.series?.points) ? data.series.points : [];

  const runsKpi = kpis.runs || {};
  const successKpi = kpis.success_rate || {};
  const inFlight = kpis.in_flight || {};
  const reviews = kpis.reviews || {};
  const securityKpi = kpis.security_stops || {};
  const cleanupKpi = kpis.cleanup_verified_rate || {};
  const p50Kpi = kpis.p50_duration_ms || {};
  const p95Kpi = kpis.p95_duration_ms || {};
  const tokensKpi = kpis.tokens || {};
  const costKpi = kpis.cost_usd || {};

  const inFlightAvailable = inFlight.available !== false;
  const inFlightCount = Number(inFlight.value) || 0;
  const inFlightPaused = Number(inFlight.paused) || 0;

  const reviewsPending = Number(reviews.value) || 0;
  const reviewsOldestWait = Number(reviews.oldest_wait_ms) || 0;
  const reviewsPausedLive = Number(reviews.paused_live) || 0;

  const totalSpark = sparkValues(points, (point) => Number(point.total) || 0);
  const successSpark = sparkValues(
    points.filter((point) => (Number(point.total) || 0) > 0),
    (point) => (Number(point.succeeded) || 0) / (Number(point.total) || 1),
  );
  const securitySpark = sparkValues(points, (point) => Number(point.security) || 0);
  const durationSpark = sparkValues(points, (point) => Number(point.p50_duration_ms) || 0).filter(
    (value) => value > 0,
  );

  return (
    <section aria-label="Key factory metrics">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <KpiTile
          label={KPI_LABELS[0]}
          value={runsKpi.available === false ? <Unavailable label="Unavailable" /> : formatNumber(runsKpi.value)}
          hint={
            runsKpi.available === false
              ? truncate(runsKpi.note || "Run counts are unavailable.", 96)
              : comparisonHint(runsKpi)
          }
          delta={deltaArg(runsKpi)}
          tone="primary"
          icon={<Play />}
          spark={totalSpark}
          href="#/runs"
          className="h-full"
        />

        <KpiTile
          label={KPI_LABELS[1]}
          value={percentValue(successKpi)}
          hint={
            successKpi.available === false
              ? truncate(successKpi.note || "No finished runs in this window.", 96)
              : `${formatNumber(successKpi.sample)} finished runs`
          }
          delta={deltaArg(successKpi)}
          tone={successKpi.available === false ? "muted" : "success"}
          icon={<CircleCheck />}
          spark={successSpark}
          className="h-full"
        />

        <KpiTile
          label={KPI_LABELS[2]}
          value={inFlightAvailable ? formatNumber(inFlightCount) : <Unavailable label="Unavailable" />}
          hint={
            inFlightAvailable
              ? inFlightPaused > 0
                ? `${formatNumber(inFlightPaused)} paused for human review`
                : "Live orchestration workflows"
              : truncate(inFlight.note || "Live orchestration state is unavailable.", 96)
          }
          tone={inFlightAvailable && inFlightCount > 0 ? "info" : "muted"}
          icon={<Radio />}
          className="h-full"
        />

        <KpiTile
          label={KPI_LABELS[3]}
          value={formatNumber(reviewsPending)}
          hint={reviews.note || "Changes waiting for a human decision"}
          tone={reviewsPending > 0 ? "warning" : "muted"}
          icon={<ClipboardCheck />}
          href="#/reviews"
          className="h-full"
          footer={
            <>
              <Hourglass aria-hidden="true" className="h-3 w-3 shrink-0" />
              <span className="truncate">
                Oldest wait{" "}
                <span className="tabular font-medium text-foreground">
                  {reviewsPending > 0 && reviewsOldestWait > 0 ? formatDuration(reviewsOldestWait) : "—"}
                </span>
              </span>
              {reviewsPausedLive > 0 ? (
                <span className="ml-auto shrink-0 tabular text-muted-foreground">{reviewsPausedLive} live paused</span>
              ) : null}
            </>
          }
        />

      </div>
      <details className="metrics-details mt-4 rounded-xl border border-border bg-surface/60">
        <summary className="flex cursor-pointer items-center gap-2 rounded-xl px-4 py-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground">
          <SlidersHorizontal aria-hidden="true" className="h-3.5 w-3.5" />
          Performance & resources
          <span className="hidden font-normal sm:inline">· Security, cleanup, latency and spend</span>
          <ChevronDown aria-hidden="true" className="details-chevron ml-auto h-4 w-4 shrink-0 transition-transform" />
        </summary>
        <div className="grid grid-cols-1 gap-3 border-t border-border p-3 sm:grid-cols-2 xl:grid-cols-3">
          <KpiTile
            label={KPI_LABELS[4]}
            value={securityKpi.available === false ? <Unavailable label="Unavailable" /> : formatNumber(securityKpi.value)}
            hint={
              Number(securityKpi.value) > 0
                ? `Among ${formatNumber(securityKpi.sample)} runs in this window`
                : Number(securityKpi.sample) > 0
                  ? `No security stop among ${formatNumber(securityKpi.sample)} runs`
                  : "No runs evaluated in this window"
            }
            delta={deltaArg(securityKpi, { security: true })}
            tone={Number(securityKpi.value) > 0 ? "danger" : "muted"}
            icon={<ShieldAlert />}
            spark={securitySpark}
            href="#/security"
            className="h-full"
          />

          <KpiTile
            label={KPI_LABELS[5]}
            value={percentValue(cleanupKpi)}
            hint={
              cleanupKpi.available === false
                ? truncate(cleanupKpi.note || "No cleanup evidence in this window.", 96)
                : `${formatNumber(cleanupKpi.sample)} runs with cleanup evidence`
            }
            delta={deltaArg(cleanupKpi)}
            tone={cleanupKpi.available === false ? "muted" : "success"}
            icon={<PackageCheck />}
            href="#/operations"
            className="h-full"
          />

          <KpiTile
            label={KPI_LABELS[6]}
            value={durationValue(p50Kpi)}
            hint={
              p50Kpi.available === false
                ? truncate(p50Kpi.note || "No duration samples in this window.", 96)
                : `${formatNumber(p50Kpi.sample)} duration samples`
            }
            delta={deltaArg(p50Kpi, { duration: true })}
            icon={<Timer />}
            spark={durationSpark}
            className="h-full"
          />

          <KpiTile
            label={KPI_LABELS[7]}
            value={durationValue(p95Kpi)}
            hint={
              p95Kpi.available === false
                ? truncate(p95Kpi.note || "No duration samples in this window.", 96)
                : `${formatNumber(p95Kpi.sample)} duration samples`
            }
            delta={deltaArg(p95Kpi, { duration: true })}
            icon={<Gauge />}
            className="h-full"
          />

          <KpiTile
            label={KPI_LABELS[8]}
            value={
              tokensKpi.available === false ? (
                <Unavailable label="Not reported" />
              ) : (
                formatNumber(tokensKpi.value, { compact: true })
              )
            }
            hint={
              tokensKpi.available === false
                ? truncate(tokensKpi.note || "No harness reported token usage.", 96)
                : `${formatNumber(tokensKpi.sample)} runs reported token usage`
            }
            delta={deltaArg(tokensKpi, { neutral: true })}
            icon={<Hash />}
            className="h-full"
          />

          <KpiTile
            label={KPI_LABELS[9]}
            value={costKpi.available === false ? <Unavailable label="Not reported" /> : formatUsd(costKpi.value)}
            hint={
              costKpi.available === false
                ? truncate(costKpi.note || "No harness reported inference cost.", 96)
                : `${formatNumber(costKpi.sample)} runs reported inference cost`
            }
            delta={deltaArg(costKpi, { neutral: true })}
            icon={<CircleDollarSign />}
            className="h-full"
          />
        </div>
      </details>
    </section>
  );
}
