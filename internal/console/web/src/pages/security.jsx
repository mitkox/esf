import { useRef, useState } from "react";
import clsx from "clsx";
import {
  Ban,
  BellOff,
  Database,
  FileWarning,
  ListChecks,
  Network,
  Radar,
  RefreshCw,
  ScanSearch,
  ShieldAlert,
  ShieldX,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { DefinitionList } from "@/components/ops/definition-list";
import { KpiTile } from "@/components/ui/kpi-tile";
import { Notice } from "@/components/ops/notice";
import { PageHeader } from "@/components/ui/page-header";
import { KpiSkeleton, TableSkeleton } from "@/components/ui/skeleton";
import { useApi } from "@/lib/hooks";
import { useWorkspace } from "@/lib/workspace";
import { formatDateTime, formatDuration, formatNumber, timeAgo, windowLabel } from "@/lib/format";
import { PosturePanel } from "@/components/ops/posture-panel";
import {
  AlertsList,
  EgressObservationsTable,
  FindingsTable,
  RulesTable,
  StopsTable,
} from "@/components/ops/security-tables";
import { asArray, asNumber, asObject, dateTimeOr, timeAgoOr, windowInfo } from "@/components/ops/data";

function toneFor(value, tone) {
  return value !== null && value > 0 ? tone : "muted";
}

function metric(value, { danger = false, good = false } = {}) {
  const number = asNumber(value);
  if (number === null) return <span className="text-muted-foreground">—</span>;
  const tone = number > 0 ? (danger ? "text-danger" : "text-foreground") : good ? "text-success" : "text-foreground";
  return <span className={clsx("tabular font-semibold", tone)}>{formatNumber(number)}</span>;
}

function SecuritySkeleton() {
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4 2xl:grid-cols-7">
        {[0, 1, 2, 3, 4, 5, 6].map((item) => (
          <KpiSkeleton key={item} />
        ))}
      </div>
      <TableSkeleton rows={4} columns={7} />
      <div className="grid gap-4 xl:grid-cols-2">
        <TableSkeleton rows={4} columns={5} />
        <TableSkeleton rows={4} columns={4} />
      </div>
    </div>
  );
}

export function SecurityPage() {
  const { window: windowKey, autoRefresh, refreshNonce } = useWorkspace();
  const api = useApi("/security", {
    params: { window: windowKey },
    pollMs: autoRefresh ? 30000 : 0,
    deps: [refreshNonce],
  });
  const [showAllObservations, setShowAllObservations] = useState(false);
  const alertsRef = useRef(null);

  const response = asObject(api.data);
  const hasData = Boolean(api.data);
  const windowData = windowInfo(response.window);
  const coverageLabel = windowData.key ? windowData.label : windowLabel(windowKey);
  const inspected = asNumber(response.inspected);
  const undelivered = asNumber(response.alerts_undelivered);
  const rules = asArray(response.rules);
  const entries = asArray(response.entries);
  const observations = asArray(response.egress_observations);
  const alerts = asArray(response.alerts);
  const stops = asArray(response.stops);
  const egress = asObject(response.egress);
  const posture = asObject(response.posture);
  const index = asObject(response.index);
  const alertsUndelivered = undelivered ?? 0;
  const visibleObservations = showAllObservations ? observations : observations.slice(0, 20);
  const updatedLabel = api.updatedAt ? timeAgo(api.updatedAt) : null;

  const alertDeliveryLabel = !alerts.length
    ? "No alerts"
    : alertsUndelivered > 0
      ? `${formatNumber(alertsUndelivered)} undelivered`
      : "All delivered";
  const alertDeliveryVariant = !alerts.length ? "outline" : alertsUndelivered > 0 ? "danger" : "success";
  const webhookNote =
    posture.alert_webhook_configured === true
      ? "Alert webhook configured"
      : posture.alert_webhook_configured === false
        ? "No alert webhook configured"
        : "Alert delivery not reported";

  const kpis = [
    {
      label: "Inspected runs",
      value: inspected,
      icon: <ScanSearch />,
      tone: "info",
      hint: "Run manifests covered by the selected window",
    },
    {
      label: "Runs with findings",
      value: asNumber(response.runs_with_findings),
      icon: <FileWarning />,
      tone: toneFor(asNumber(response.runs_with_findings), "danger"),
      hint: "Runs with at least one behavior finding",
    },
    {
      label: "Tripped",
      value: asNumber(response.tripped),
      icon: <ShieldAlert />,
      tone: toneFor(asNumber(response.tripped), "danger"),
      hint: "Runs quarantined by the behavior monitor",
    },
    {
      label: "Blocked by agent",
      value: asNumber(response.blocked),
      icon: <Ban />,
      tone: toneFor(asNumber(response.blocked), "warning"),
      hint: "Agent used the documented stop signal",
    },
    {
      label: "Gate tampered",
      value: asNumber(response.gate_tampered),
      icon: <ShieldX />,
      tone: toneFor(asNumber(response.gate_tampered), "danger"),
      hint: "Patches that modified their own verification gates",
    },
    {
      label: "Egress violations",
      value: asNumber(response.egress_violations),
      icon: <Network />,
      tone: toneFor(asNumber(response.egress_violations), "security"),
      hint: "Sandbox boundary proved violated by evidence",
    },
    {
      label: "Alerts undelivered",
      value: undelivered,
      icon: <BellOff />,
      tone: toneFor(undelivered, "warning"),
      hint: "Recorded alerts without successful delivery",
      footer: webhookNote,
    },
  ];

  const egressItems = [
    {
      label: "Probed runs",
      value: metric(egress.probed),
      hint: "Runs with a recorded egress probe",
    },
    {
      label: "Canary reachable",
      value: metric(egress.canary_reachable),
      hint: "Expected under an allow policy; a violation only under a deny policy",
    },
    {
      label: "Metadata reachable",
      value: metric(egress.metadata_reachable, { danger: true, good: true }),
      hint: "Cloud metadata endpoint reachable; evidence expects this to be blocked",
    },
    {
      label: "Denied expected",
      value: metric(egress.denied_expected),
      hint: "Observations where the policy intended to deny egress",
    },
    {
      label: "Inconclusive",
      value: metric(egress.inconclusive),
      hint: "No usable verdict; never evidence of a healthy boundary",
    },
    {
      label: "Violations",
      value: metric(egress.violations, { danger: true }),
      hint: "Violations proved by probe evidence",
    },
  ];

  const coverageItems = [
    {
      label: "Coverage window",
      value: coverageLabel,
      hint:
        windowData.start && windowData.end
          ? `${formatDateTime(windowData.start)} → ${formatDateTime(windowData.end)}`
          : undefined,
    },
    { label: "Inspected runs", value: formatNumber(inspected, { fallback: "—" }) },
    {
      label: "Evidence index",
      value: `${formatNumber(index.runs, { fallback: "—" })} runs · ${formatNumber(index.changes, { fallback: "—" })} changes`,
      hint: `Refreshed ${timeAgoOr(index.refreshed_at)} · scan ${formatDuration(index.scan_ms)}`,
    },
    {
      label: "Index scan errors",
      value: formatNumber(index.scan_errors, { fallback: "—" }),
      hint: asNumber(index.scan_errors) > 0 ? "The index skipped unreadable evidence" : "No scan errors reported",
    },
    {
      label: "Oldest / newest run",
      value: `${formatDateTime(index.oldest_run_at)} → ${formatDateTime(index.newest_run_at)}`,
      span: 2,
    },
    { label: "Generated at", value: dateTimeOr(response.generated_at) },
  ];

  return (
    <div className="mx-auto w-full max-w-[1600px] px-4 py-5 sm:px-6 lg:px-8">
      <PageHeader
        eyebrow="Governance · Security"
        title="Security"
        description="Cross-run hardening evidence: behavior findings, gate integrity, agent stops, egress observations and alert delivery. Every figure is derived from stored run manifests — the console never estimates a security outcome."
        meta={
          <>
            <Badge variant="security" size="sm" dot>
              {coverageLabel}
            </Badge>
            <span>
              Inspected{" "}
              <span className="tabular font-medium text-foreground">
                {formatNumber(inspected, { fallback: "—" })}
              </span>{" "}
              runs
            </span>
            <span aria-hidden="true" className="text-border-strong">
              ·
            </span>
            <span>Generated {dateTimeOr(response.generated_at)}</span>
            {updatedLabel ? (
              <>
                <span aria-hidden="true" className="text-border-strong">
                  ·
                </span>
                <span>Updated {updatedLabel}</span>
              </>
            ) : null}
          </>
        }
        actions={
          <Button
            variant="secondary"
            size="sm"
            icon={<RefreshCw />}
            onClick={api.refresh}
            loading={api.loading && hasData}
          >
            Refresh
          </Button>
        }
      />

      <div className="space-y-4">
        {api.error && hasData ? (
          <Notice
            tone="warning"
            title="Live refresh failed — showing the last successful snapshot"
            announce
            action={
              <Button variant="secondary" size="xs" onClick={api.refresh}>
                Retry
              </Button>
            }
          >
            {api.error.message || "The security endpoint could not be reached."} This view may be{" "}
            {updatedLabel || "stale"}.
          </Notice>
        ) : null}

        {api.error && !hasData ? (
          <Notice
            tone="danger"
            title="Security evidence could not be loaded"
            announce
            action={
              <Button variant="secondary" size="xs" onClick={api.refresh}>
                Retry
              </Button>
            }
          >
            {api.error.message || "The security endpoint could not be reached."}
          </Notice>
        ) : null}

        {!hasData && api.loading ? <SecuritySkeleton /> : null}

        {hasData ? (
          <>
            <PosturePanel
              posture={posture}
              alertsUndelivered={undelivered}
              inspected={inspected}
              onViewAlerts={() => alertsRef.current?.scrollIntoView({ behavior: "smooth", block: "start" })}
            />

            <div className="grid grid-cols-2 gap-3 lg:grid-cols-4 2xl:grid-cols-7">
              {kpis.map((kpi) => (
                <KpiTile
                  key={kpi.label}
                  label={kpi.label}
                  value={formatNumber(kpi.value, { fallback: "—" })}
                  hint={kpi.hint}
                  tone={kpi.tone}
                  icon={kpi.icon}
                  footer={kpi.footer}
                />
              ))}
            </div>

            <Card>
              <CardHeader
                icon={<FileWarning />}
                title="Runs with security signals"
                description="Runs whose result, findings, gate integrity or egress probe produced a security signal. Newest first."
                actions={
                  <Badge variant="outline" size="sm">
                    {formatNumber(entries.length)} shown
                  </Badge>
                }
              />
              <FindingsTable
                entries={entries}
                emptyDescription={
                  inspected === null
                    ? "The response did not report an inspected-run count or any security signals."
                    : `All ${formatNumber(inspected)} runs in this window are free of recorded security signals.`
                }
              />
              <CardFooter>
                Aggregate counts cover every inspected run; this list is a bounded triage view (API cap 100).
              </CardFooter>
            </Card>

            <div className="grid gap-4 xl:grid-cols-12">
              <Card className="xl:col-span-7">
                <CardHeader
                  icon={<ListChecks />}
                  title="Behavior rules"
                  description="Findings aggregated by rule across every inspected run."
                  actions={
                    <Badge variant="outline" size="sm">
                      {formatNumber(rules.length)} rule{rules.length === 1 ? "" : "s"}
                    </Badge>
                  }
                />
                <RulesTable rules={rules} />
                <CardFooter>Sorted by finding count, then rule id.</CardFooter>
              </Card>

              <Card className="xl:col-span-5">
                <CardHeader
                  icon={<Radar />}
                  title="Egress probe aggregate"
                  description="Measured sandbox network posture across probed runs."
                />
                <CardBody>
                  <DefinitionList items={egressItems} columns={2} />
                </CardBody>
                <CardFooter>
                  Inconclusive observations mean the probe produced no verdict — they are not a healthy result.
                </CardFooter>
              </Card>
            </div>

            <Card>
              <CardHeader
                icon={<Network />}
                title="Egress observations"
                description="Per-run network probe results. Undecided outcomes are reported as undecided, never as passed."
                actions={
                  <Badge variant="outline" size="sm">
                    {formatNumber(observations.length)} of {formatNumber(egress.probed, { fallback: "—" })} probed
                  </Badge>
                }
              />
              <EgressObservationsTable observations={visibleObservations} />
              {observations.length ? (
                <CardFooter>
                  {observations.length > 20 ? (
                    <Button
                      variant="ghost"
                      size="xs"
                      onClick={() => setShowAllObservations((value) => !value)}
                    >
                      {showAllObservations
                        ? "Show fewer observations"
                        : `Show all ${formatNumber(observations.length)} observations`}
                    </Button>
                  ) : null}
                  <span className={clsx(observations.length > 20 && "sm:ml-auto")}>
                    agent_uid 0 means the agent ran as root; undecided probes are not healthy evidence.
                  </span>
                </CardFooter>
              ) : null}
            </Card>

            <div ref={alertsRef} className="grid scroll-mt-24 gap-4 xl:grid-cols-2">
              <Card>
                <CardHeader
                  icon={<BellOff />}
                  title="Security alerts"
                  description="Alert records from run evidence with delivery status."
                  actions={
                    <Badge variant={alertDeliveryVariant} size="sm" dot>
                      {alertDeliveryLabel}
                    </Badge>
                  }
                />
                <AlertsList alerts={alerts} />
                <CardFooter>
                  Delivery requires a configured hardening alert webhook; undelivered alerts remain in evidence only.
                </CardFooter>
              </Card>

              <Card>
                <CardHeader
                  icon={<ShieldX />}
                  title="Security stops"
                  description="Runs stopped by quarantine, gate tampering or unverified egress."
                  actions={
                    <Badge variant="outline" size="sm">
                      {formatNumber(stops.length)} run{stops.length === 1 ? "" : "s"}
                    </Badge>
                  }
                />
                <StopsTable stops={stops} />
              </Card>
            </div>

            <Card>
              <CardHeader
                icon={<Database />}
                title="Coverage and evidence sources"
                description="Every figure on this page is derived from stored run manifests; the console never estimates a security outcome."
              />
              <CardBody>
                <DefinitionList items={coverageItems} columns={3} />
              </CardBody>
              <CardFooter>
                Source: runs/*/run.json hardening evidence · generated {dateTimeOr(response.generated_at)}
              </CardFooter>
            </Card>
          </>
        ) : null}
      </div>
    </div>
  );
}
