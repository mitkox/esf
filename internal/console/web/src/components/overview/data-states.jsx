import clsx from "clsx";
import { CircleAlert, Database, Info, RefreshCw, TriangleAlert } from "lucide-react";
import { Card, CardBody } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { ChartSkeleton, KpiSkeleton, Skeleton, SkeletonText, TableSkeleton } from "@/components/ui/skeleton";
import { formatNumber, timeAgo } from "@/lib/format";

const KPI_SLOTS = 10;

// OverviewSkeleton mirrors the real dashboard silhouette so a slow artifact
// store never causes a layout jump.
export function OverviewSkeleton() {
  return (
    <div className="space-y-4" role="status" aria-label="Loading factory overview">
      <div className="grid grid-cols-2 gap-3 xl:grid-cols-5">
        {Array.from({ length: KPI_SLOTS }).map((_, index) => (
          <KpiSkeleton key={index} />
        ))}
      </div>

      <div className="grid-12">
        <ChartSkeleton className="col-span-12 xl:col-span-7" height={224} />
        <ChartSkeleton className="col-span-12 xl:col-span-5" height={224} />
      </div>

      <div className="grid-12">
        {[0, 1, 2].map((index) => (
          <Card key={index} className="col-span-12 flex flex-col lg:col-span-6 xl:col-span-4">
            <div className="border-b border-border px-5 py-3">
              <Skeleton className="h-3 w-32" />
              <Skeleton className="mt-2 h-2.5 w-48" />
            </div>
            <CardBody className="space-y-2.5">
              <SkeletonText lines={6} />
            </CardBody>
          </Card>
        ))}
      </div>

      <div className="grid-12">
        <Card className="col-span-12 lg:col-span-6">
          <div className="border-b border-border px-5 py-3">
            <Skeleton className="h-3 w-28" />
          </div>
          <CardBody className="space-y-2">
            <SkeletonText lines={7} />
          </CardBody>
        </Card>
        <Card className="col-span-12 lg:col-span-6">
          <div className="border-b border-border px-5 py-3">
            <Skeleton className="h-3 w-32" />
          </div>
          <CardBody className="space-y-2">
            <SkeletonText lines={7} />
          </CardBody>
        </Card>
      </div>

      <TableSkeleton rows={6} columns={6} />
    </div>
  );
}

// OverviewError is the blocking first-load failure: explicit, actionable and
// honest about what did not load.
export function OverviewError({ error, onRetry, onRetryPending = false }) {
  const status = error?.status ? `HTTP ${error.status}` : "";
  const code = error?.code ? `code ${error.code}` : "";
  const technical = [status, code].filter(Boolean).join(" · ");
  return (
    <Card className="border-danger/30" role="alert">
      <CardBody className="flex flex-col gap-4 py-8 sm:flex-row sm:items-start">
        <span className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-danger/10 text-danger">
          <CircleAlert aria-hidden="true" className="h-4 w-4" />
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="text-sm font-semibold text-foreground">The overview could not be loaded</h2>
          <p className="mt-1.5 text-[12px] leading-relaxed text-muted-foreground">
            {error?.message || "The console could not reach the factory API."} The rest of the console remains
            available; runs, changes and other screens are unaffected.
          </p>
          {technical ? <p className="mono mt-2 text-[10px] text-muted-foreground">{technical}</p> : null}
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Button variant="secondary" size="sm" icon={<RefreshCw />} onClick={onRetry} loading={onRetryPending}>
              Retry
            </Button>
            <Button variant="ghost" size="sm" href="#/runs">
              Open runs ledger
            </Button>
          </div>
        </div>
      </CardBody>
    </Card>
  );
}

// StaleEvidenceBanner reports an index scan failure: the console is still
// showing the last complete projection, and the operator must be able to see
// that it is stale.
export function StaleEvidenceBanner({ index, onRetry, className }) {
  if (!index?.stale) return null;
  const scanned = Number(index?.scan_errors) || 0;
  return (
    <div
      role="alert"
      className={clsx(
        "mb-4 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-panel border border-warning/30 bg-warning/5 px-4 py-2.5",
        className,
      )}
    >
      <TriangleAlert aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-warning" />
      <p className="min-w-0 flex-1 text-[11px] leading-5 text-muted-foreground">
        <span className="font-semibold text-foreground">Evidence index is stale.</span>{" "}
        {index.scan_error || `The last scan could not read ${scanned} evidence entr${scanned === 1 ? "y" : "ies"}.`}{" "}
        Numbers below come from the last complete scan
        {index.refreshed_at ? ` (${formatNumber(index.runs)} runs, refreshed ${formatAgo(index.refreshed_at)})` : ""}.
      </p>
      {onRetry ? (
        <Button variant="ghost" size="xs" icon={<RefreshCw />} onClick={onRetry} className="ml-auto">
          Retry
        </Button>
      ) : null}
    </div>
  );
}

function formatAgo(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "at an unknown time";
  return timeAgo(value);
}

// RefreshFailureBanner keeps the last successful snapshot on screen when a
// background poll or manual refresh fails.
export function RefreshFailureBanner({ error, updatedAt, updatedLabel, onRetry }) {
  return (
    <div
      role="status"
      className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-panel border border-warning/30 bg-warning/5 px-4 py-2.5"
    >
      <TriangleAlert aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-warning" />
      <p className="min-w-0 flex-1 text-[11px] leading-5 text-muted-foreground">
        <span className="font-semibold text-foreground">Refresh failed.</span>{" "}
        {updatedAt ? `Showing the last successful snapshot from ${updatedLabel}. ` : "No snapshot has been received yet. "}
        {error?.message || "The factory API did not answer."}
      </p>
      <Button variant="ghost" size="xs" icon={<RefreshCw />} onClick={onRetry} className="ml-auto">
        Retry
      </Button>
    </div>
  );
}

// EmptyEvidence is the whole-store empty state: the console is working, the
// factory simply has not recorded a run yet.
export function EmptyEvidence({ index }) {
  const scanned = Number(index?.scan_errors) || 0;
  return (
    <Card>
      <CardBody className="py-12">
        <EmptyState
          icon={<Database aria-hidden="true" className="h-4 w-4" />}
          title="No factory evidence yet"
          description="The console reads run manifests and change records from the configured evidence store. Once the factory records its first run, the metrics, charts and attention queue fill in automatically."
          action={
            <div className="flex flex-wrap items-center justify-center gap-2">
              <Button variant="secondary" size="sm" href="#/operations">
                Open operations
              </Button>
              <Button variant="ghost" size="sm" href="#/runs">
                Open runs ledger
              </Button>
            </div>
          }
        />
        {scanned > 0 ? (
          <p className="mx-auto mt-4 max-w-md text-center text-[11px] text-warning">
            {index?.scan_error || `${scanned} evidence file${scanned === 1 ? "" : "s"} could not be read during the last index scan.`}
          </p>
        ) : null}
      </CardBody>
    </Card>
  );
}

// WindowEmptyNotice explains a zero-run window without pretending the factory
// is idle: the evidence store may simply predate or postdate the window.
export function WindowEmptyNotice({ currentWindow, windowName, storeRuns, onSelectWindow, className }) {
  const suggestions = [
    { id: "30d", label: "Last 30 days" },
    { id: "all", label: "All time" },
  ].filter((option) => option.id !== currentWindow);
  return (
    <div
      role="status"
      className={clsx(
        "flex flex-wrap items-center gap-x-3 gap-y-2 rounded-panel border border-info/25 bg-info/5 px-4 py-2.5",
        className,
      )}
    >
      <Info aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-info" />
      <p className="min-w-0 flex-1 text-[11px] leading-5 text-muted-foreground">
        <span className="font-semibold text-foreground">No runs started in {windowName.toLowerCase()}.</span>{" "}
        {storeRuns > 0
          ? `The evidence store still holds ${formatNumber(storeRuns)} run${storeRuns === 1 ? "" : "s"} from other periods; widen the window to see them.`
          : "The store has not recorded run evidence yet."}
      </p>
      <div className="flex shrink-0 items-center gap-1.5">
        {suggestions.map((option) => (
          <Button
            key={option.id}
            variant="ghost"
            size="xs"
            onClick={() => onSelectWindow(option.id)}
            className="text-info hover:text-info"
          >
            {option.label}
          </Button>
        ))}
      </div>
    </div>
  );
}

// TrustNote is the tiny provenance label used under analytical cards.
export function TrustNote({ children, className }) {
  return <span className={clsx("text-[10px] leading-4 text-muted-foreground/80", className)}>{children}</span>;
}

export function ScanErrorBadge({ scanErrors }) {
  const count = Number(scanErrors) || 0;
  if (!count) return null;
  return (
    <Badge variant="warning" size="xs" dot title="Evidence files that could not be read during the last index scan">
      {count} scan error{count === 1 ? "" : "s"}
    </Badge>
  );
}
