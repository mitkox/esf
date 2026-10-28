import { ChartColumn } from "lucide-react";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import {
  StackedBarChart,
  StackedChartLegend,
  throughputTotals,
} from "@/components/charts/stacked-bar-chart";
import { formatNumber, titleCase } from "@/lib/format";

const UNIT_LABEL = { hour: "hour", day: "day", month: "month" };
const BUCKET_LABEL = { hour: "hourly", day: "daily", month: "monthly" };

export function ThroughputCard({ data, className }) {
  const points = Array.isArray(data?.series?.points) ? data.series.points : [];
  const granularity = data?.series?.granularity || "day";
  const totals = throughputTotals(points);
  const total = totals.total;
  const windowName = data?.window?.label || "the selected window";
  const unit = UNIT_LABEL[granularity] || "bucket";
  const bucketLabel = BUCKET_LABEL[granularity] || "bucketed";

  const peak = points.reduce((best, point) => {
    if (!best) return point;
    return (Number(point.total) || 0) > (Number(best.total) || 0) ? point : best;
  }, null);
  const peakTotal = Number(peak?.total) || 0;

  const ariaLabel =
    total > 0
      ? `Stacked bar chart of ${formatNumber(total)} runs by ${unit} for ${windowName}. ${bucketLabel} buckets: ${formatNumber(
          totals.succeeded,
        )} succeeded, ${formatNumber(totals.failed)} failed, ${formatNumber(totals.security)} security stops, ${formatNumber(
          totals.other,
        )} other.`
      : `No runs were recorded by ${unit} for ${windowName}.`;

  return (
    <Card className={`flex h-full min-w-0 flex-col ${className || ""}`}>
      <CardHeader
        icon={<ChartColumn />}
        title="Run throughput"
        description={
          total > 0
            ? `${formatNumber(total)} runs started in ${windowName.toLowerCase()}`
            : `Per-${unit} run volume for ${windowName.toLowerCase()}`
        }
        actions={
          <Badge variant="muted" size="xs">
            {titleCase(granularity)} buckets
          </Badge>
        }
      />
      <CardBody className="flex flex-1 flex-col">
        <StackedChartLegend points={points} />
        <div className="mt-3 flex-1">
          {total > 0 ? (
            <StackedBarChart
              points={points}
              granularity={granularity}
              height={232}
              ariaLabel={ariaLabel}
            />
          ) : (
            <EmptyState
              compact
              icon={<ChartColumn aria-hidden="true" className="h-4 w-4" />}
              title="No runs in this window"
              description={`The factory did not start a run in ${windowName.toLowerCase()}. Widen the window or check the runs ledger for earlier evidence.`}
            />
          )}
        </div>
      </CardBody>
      <CardFooter>
        <span className="truncate">
          {peakTotal > 0 ? (
            <>
              Peak <span className="tabular font-medium text-foreground">{formatNumber(peakTotal)}</span> on{" "}
              {peak.label}
            </>
          ) : (
            "No activity to summarise"
          )}
        </span>
        <Button href="#/runs" variant="ghost" size="xs" className="ml-auto">
          Open runs
        </Button>
      </CardFooter>
    </Card>
  );
}
