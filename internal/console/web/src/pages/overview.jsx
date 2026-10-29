import { useEffect, useState } from "react";
import clsx from "clsx";
import { Activity, CalendarRange, Clock, Cpu, GitBranch, RefreshCw } from "lucide-react";
import { useApi } from "@/lib/hooks";
import { useSession } from "@/lib/session";
import { useWorkspace } from "@/lib/workspace";
import {
  formatDate,
  formatDateTime,
  formatNumber,
  repoLabel,
  timeAgo,
  titleCase,
  windowLabel,
} from "@/lib/format";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { Badge, StatusDot } from "@/components/ui/badge";
import { TONE_BORDER, TONE_TEXT } from "@/components/ui/tones";
import { KpiGrid } from "@/components/overview/kpi-grid";
import { ThroughputCard } from "@/components/overview/throughput-card";
import { OutcomesCard } from "@/components/overview/outcomes-card";
import { AttentionPanel } from "@/components/overview/attention-panel";
import { HealthPanel } from "@/components/overview/health-panel";
import { SecurityPanel } from "@/components/overview/security-panel";
import { BreakdownCard } from "@/components/overview/breakdown-card";
import { RecentRunsCard } from "@/components/overview/recent-runs";
import { CatalogStrip } from "@/components/overview/catalog-strip";
import {
  EmptyEvidence,
  OverviewError,
  OverviewSkeleton,
  RefreshFailureBanner,
  StaleEvidenceBanner,
  WindowEmptyNotice,
} from "@/components/overview/data-states";

const PAGE_CONTAINER = "mx-auto w-full max-w-[1600px] px-4 py-5 sm:px-6 lg:px-8";

const ENVIRONMENT_LABEL = {
  production: "Production",
  staging: "Staging",
  development: "Development",
  local: "Local",
  self_hosted: "Self-hosted",
};

const ENVIRONMENT_TONE = {
  production: "warning",
  staging: "info",
  development: "info",
  local: "muted",
  self_hosted: "muted",
};

const HEALTH_CHIP = {
  operational: { label: "Factory operational", tone: "success" },
  degraded: { label: "Factory degraded", tone: "warning" },
  attention: { label: "Factory needs attention", tone: "danger" },
};

function HealthChip({ status, loading }) {
  const key = String(status || "").toLowerCase();
  const meta = HEALTH_CHIP[key];
  if (!meta) {
    return (
      <span className="inline-flex h-7 items-center gap-1.5 rounded-lg border border-border px-2.5 text-[11px] font-medium text-muted-foreground">
        <StatusDot tone="muted" />
        {loading ? "Checking status…" : "Health not reported"}
      </span>
    );
  }
  return (
    <a
      href="#/operations"
      title="Open operations and infrastructure health"
      className={clsx(
        "inline-flex h-7 items-center gap-1.5 rounded-lg border px-2.5 text-[11px] font-medium transition-colors hover:bg-muted",
        TONE_BORDER[meta.tone],
        TONE_TEXT[meta.tone],
      )}
    >
      <StatusDot tone={meta.tone} pulse={meta.tone === "danger"} />
      {meta.label}
    </a>
  );
}

function coverageText(windowInfo) {
  if (!windowInfo?.end) return "window unavailable";
  if (!windowInfo.start) return `all recorded evidence through ${formatDate(windowInfo.end)}`;
  return `${formatDate(windowInfo.start)} – ${formatDate(windowInfo.end)}`;
}

export function OverviewPage() {
  const { window: windowKey, setWindow, autoRefresh, refreshNonce } = useWorkspace();
  const { session } = useSession();
  const { data, error, loading, updatedAt, refresh } = useApi("/overview", {
    params: { window: windowKey },
    pollMs: autoRefresh ? 30000 : 0,
    deps: [refreshNonce],
  });
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 15000);
    return () => window.clearInterval(timer);
  }, []);

  const environmentKey = String(session?.environment || "self-hosted").toLowerCase();
  const environmentLabel = ENVIRONMENT_LABEL[environmentKey] || titleCase(environmentKey);
  const environmentTone = ENVIRONMENT_TONE[environmentKey] || "muted";

  const seriesPoints = Array.isArray(data?.series?.points) ? data.series.points : [];
  const windowRuns = seriesPoints.reduce((sum, point) => sum + (Number(point.total) || 0), 0);
  const storeRuns = Number(data?.index?.runs) || 0;
  const windowName = data?.window?.label || windowLabel(windowKey);
  const updatedLabel = updatedAt ? timeAgo(updatedAt, now) : "";

  const firstLoad = loading && !data;
  const blockingError = Boolean(error) && !data;
  const storeEmpty = Boolean(data) && storeRuns === 0;

  const harnesses = Array.isArray(data?.harnesses) ? data.harnesses : [];
  const repositories = Array.isArray(data?.repositories) ? data.repositories : [];
  const scopes = Array.isArray(data?.scopes) ? data.scopes : [];

  const scopesSummary = scopes.length ? (
    <span title={scopes.map((scope) => `${scope.name}: ${formatNumber(scope.runs)} runs`).join(" · ")}>
      Scopes:{" "}
      {scopes.map((scope, index) => (
        <span key={scope.name || index}>
          {index > 0 ? " · " : ""}
          {scope.name} <span className="tabular text-foreground">{formatNumber(scope.runs)}</span>
        </span>
      ))}
    </span>
  ) : null;

  const comparisonNote =
    windowKey === "all" ? "" : " Deltas compare with the preceding equal-length window.";
  const description = data
    ? `Read-only operational picture from run manifests, change records and live orchestration state. Evidence coverage: ${coverageText(
        data.window,
      )}.${comparisonNote}`
    : "Read-only operational picture from run manifests, change records and live orchestration state.";

  return (
    <div className={PAGE_CONTAINER}>
      <PageHeader
        eyebrow="Mission control"
        title="Factory overview"
        description={description}
        meta={
          <>
            <Badge variant="outline" size="sm" dot={environmentTone} className="capitalize">
              {environmentLabel}
            </Badge>
            <span className="inline-flex items-center gap-1.5">
              <CalendarRange aria-hidden="true" className="h-3.5 w-3.5" />
              {windowName}
            </span>
            <span
              className="inline-flex items-center gap-1.5"
              title={updatedAt ? formatDateTime(updatedAt) : "The overview has not synced yet"}
            >
              <Clock aria-hidden="true" className="h-3.5 w-3.5" />
              {updatedAt ? `Updated ${updatedLabel}` : loading ? "Syncing…" : "Not yet synced"}
            </span>
            <span className="inline-flex items-center gap-1.5">
              <Activity aria-hidden="true" className="h-3.5 w-3.5" />
              {autoRefresh ? "Auto-refresh 30s" : "Auto-refresh off"}
            </span>
            {data?.index ? (
              <span className="hidden text-muted-foreground/80 sm:inline">
                {formatNumber(storeRuns)} runs · {formatNumber(Number(data.index.changes) || 0)} changes indexed
              </span>
            ) : null}
          </>
        }
        actions={
          <>
            <HealthChip status={data?.health?.status} loading={firstLoad} />
            <Button
              variant="secondary"
              size="sm"
              icon={<RefreshCw className={clsx(loading && "animate-spin")} />}
              onClick={refresh}
              disabled={firstLoad}
              aria-label="Refresh overview data"
            >
              Refresh
            </Button>
          </>
        }
      />

      {data ? <StaleEvidenceBanner index={data?.index} onRetry={refresh} /> : null}

      {error && data ? (
        <RefreshFailureBanner
          error={error}
          updatedAt={updatedAt}
          updatedLabel={updatedLabel || "the last successful poll"}
          onRetry={refresh}
        />
      ) : null}

      {firstLoad ? (
        <OverviewSkeleton />
      ) : blockingError ? (
        <OverviewError error={error} onRetry={refresh} onRetryPending={loading} />
      ) : storeEmpty ? (
        <EmptyEvidence index={data?.index} />
      ) : (
        <div className="space-y-4">
          <KpiGrid data={data} />

          {windowRuns === 0 ? (
            <WindowEmptyNotice
              currentWindow={windowKey}
              windowName={windowName}
              storeRuns={storeRuns}
              onSelectWindow={setWindow}
            />
          ) : null}

          <div className="grid-12">
            <ThroughputCard data={data} className="col-span-12 min-w-0 xl:col-span-7" />
            <OutcomesCard data={data} className="col-span-12 min-w-0 xl:col-span-5" />

            <AttentionPanel
              items={data?.attention}
              now={now}
              className="col-span-12 min-w-0 lg:col-span-6 xl:col-span-4"
            />
            <HealthPanel health={data?.health} className="col-span-12 min-w-0 lg:col-span-6 xl:col-span-4" />
            <SecurityPanel security={data?.security} className="col-span-12 min-w-0 xl:col-span-4" />

            <BreakdownCard
              title="Harnesses"
              description={`Agent harness volume in ${windowName.toLowerCase()}`}
              icon={<Cpu />}
              items={harnesses}
              labelOf={(item) => item.name || "unassigned"}
              emptyLabel="No harness recorded runs in this window."
              note="Bars scale to the busiest harness"
              extras={
                harnesses.length
                  ? `${formatNumber(harnesses.length)} harness${harnesses.length === 1 ? "" : "es"} with runs`
                  : null
              }
              href="#/operations"
              hrefLabel="Operations"
              className="col-span-12 min-w-0 lg:col-span-6"
            />
            <BreakdownCard
              title="Repositories"
              description={`Repository volume in ${windowName.toLowerCase()}`}
              icon={<GitBranch />}
              items={repositories}
              labelOf={(item) => repoLabel(item.name)}
              emptyLabel="No repository recorded runs in this window."
              note="Bars scale to the busiest repository"
              extras={scopesSummary}
              href="#/runs"
              hrefLabel="Runs"
              className="col-span-12 min-w-0 lg:col-span-6"
            />
          </div>

          <RecentRunsCard runs={data?.recent_runs} now={now} />

          <CatalogStrip catalog={data?.catalog} />
        </div>
      )}
    </div>
  );
}
