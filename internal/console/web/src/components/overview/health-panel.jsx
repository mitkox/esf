import clsx from "clsx";
import { Activity, ArrowRight } from "lucide-react";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { Badge, StatusDot } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TONE_TEXT } from "@/components/ui/tones";
import { formatDateTime, formatNumber, timeAgo } from "@/lib/format";
import { ScanErrorBadge } from "./data-states";

const STATUS_META = {
  operational: { label: "Operational", tone: "success", note: "Every health check passed." },
  degraded: { label: "Degraded", tone: "warning", note: "At least one check or probe needs review." },
  attention: { label: "Needs attention", tone: "danger", note: "At least one health check is reporting an error." },
};

const CHECK_META = {
  ok: { label: "OK", tone: "success" },
  warn: { label: "Warn", tone: "warning" },
  error: { label: "Error", tone: "danger" },
  unknown: { label: "Unknown", tone: "muted" },
  disabled: { label: "Disabled", tone: "muted" },
};

const PROBE_META = {
  reachable: { label: "Reachable", tone: "success" },
  unreachable: { label: "Unreachable", tone: "danger" },
  unknown: { label: "Unknown", tone: "muted" },
};

function statusMeta(status) {
  return STATUS_META[String(status || "").toLowerCase()] || { label: "Unknown", tone: "muted", note: "Health was not reported." };
}

export function HealthPanel({ health, className }) {
  const status = statusMeta(health?.status);
  const checks = Array.isArray(health?.checks) ? health.checks : [];
  const probes = Array.isArray(health?.probes) ? health.probes : [];
  const index = health?.index || {};
  const scanErrors = Number(index.scan_errors) || 0;
  const unmeasured = checks.filter((check) => {
    const state = String(check.state || "").toLowerCase();
    return state === "unknown" || state === "disabled";
  }).length;
  const description =
    status.label === "Operational" && unmeasured > 0
      ? `Measured checks passed. ${unmeasured} live check${unmeasured === 1 ? " is" : "s are"} disabled or unknown, so they are not reported as passing.`
      : status.note;

  return (
    <Card className={clsx("flex h-full min-w-0 flex-col", className)}>
      <CardHeader
        icon={<Activity />}
        title="System health"
        description={description}
        actions={
          <Badge variant={status.tone === "muted" ? "muted" : status.tone} size="xs" dot>
            {status.label}
          </Badge>
        }
      />
      <CardBody className="flex flex-1 flex-col">
        {checks.length ? (
          <ul aria-label="Factory health checks" className="divide-y divide-border/60">
            {checks.map((check) => {
              const meta = CHECK_META[String(check.state || "").toLowerCase()] || CHECK_META.unknown;
              return (
                <li key={check.id || check.label} className="flex items-start gap-2.5 py-2 first:pt-0">
                  <StatusDot tone={meta.tone} className="mt-[7px]" />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-baseline justify-between gap-2">
                      <p className="truncate text-[12px] font-medium leading-5 text-foreground">{check.label || check.id}</p>
                      <span
                        className={clsx(
                          "shrink-0 text-[9px] font-semibold uppercase leading-4 tracking-[0.1em]",
                          TONE_TEXT[meta.tone],
                        )}
                      >
                        {meta.label}
                      </span>
                    </div>
                    <div className="flex flex-wrap items-baseline gap-x-2">
                      <p className="min-w-0 text-[11px] leading-4 text-muted-foreground">{check.detail || "No detail reported."}</p>
                      {Number(check.latency_ms) > 0 ? (
                        <span className="tabular shrink-0 text-[10px] leading-4 text-muted-foreground/80">
                          {formatNumber(check.latency_ms)}ms
                        </span>
                      ) : null}
                    </div>
                  </div>
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="py-2 text-[11px] text-muted-foreground">No health checks were returned for this workspace.</p>
        )}

        <div className="mt-3 border-t border-border/70 pt-3">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <p className="text-[10px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground">
              Reachability probes
            </p>
            <p className="text-[10px] leading-4 text-muted-foreground/80">TCP dial from the console host</p>
          </div>
          {probes.length ? (
            <ul aria-label="Reachability probes" className="mt-1.5 space-y-1">
              {probes.map((probe) => {
                const meta = PROBE_META[String(probe.state || "").toLowerCase()] || PROBE_META.unknown;
                return (
                  <li key={probe.id || probe.label} className="flex items-center gap-2 py-0.5">
                    <StatusDot tone={meta.tone} size="xs" />
                    <span className="min-w-0 shrink-0 truncate text-[11px] font-medium leading-4 text-foreground">
                      {probe.label || probe.id}
                    </span>
                    <span className="mono min-w-0 flex-1 truncate text-[10px] leading-4 text-muted-foreground" title={probe.target}>
                      {probe.target}
                    </span>
                    <span className={clsx("shrink-0 text-[10px] font-medium leading-4", TONE_TEXT[meta.tone])}>
                      {meta.label}
                    </span>
                    {Number(probe.latency_ms) > 0 ? (
                      <span className="tabular shrink-0 text-[10px] leading-4 text-muted-foreground">
                        {formatNumber(probe.latency_ms)}ms
                      </span>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          ) : (
            <p className="mt-1.5 text-[11px] leading-4 text-muted-foreground">
              No probe targets are configured for this deployment.
            </p>
          )}
          <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground/80">
            Reachability is a bounded probe, not a health claim about the target service.
          </p>
        </div>
      </CardBody>
      <CardFooter className="mt-auto">
        <span className="min-w-0 truncate" title={index.refreshed_at ? formatDateTime(index.refreshed_at) : undefined}>
          Index refreshed {index.refreshed_at ? timeAgo(index.refreshed_at) : "—"}
          {Number(index.scan_ms) > 0 ? ` · scan ${formatNumber(index.scan_ms)}ms` : ""}
          {Number(index.runs) || Number(index.changes)
            ? ` · ${formatNumber(index.runs || 0)} runs / ${formatNumber(index.changes || 0)} changes`
            : ""}
        </span>
        {scanErrors > 0 ? <ScanErrorBadge scanErrors={scanErrors} /> : null}
        <Button href="#/operations" variant="ghost" size="xs" iconRight={<ArrowRight />} className="ml-auto">
          Operations
        </Button>
      </CardFooter>
    </Card>
  );
}
