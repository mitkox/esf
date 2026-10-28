import clsx from "clsx";
import {
  Activity,
  Boxes,
  CircleAlert,
  Database,
  Gauge,
  Layers,
  ListChecks,
  PackageCheck,
  Radar,
  RefreshCw,
  Server,
  ShieldCheck,
  Workflow,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton, TableSkeleton } from "@/components/ui/skeleton";
import { StatusPill } from "@/components/ui/status-pill";
import { TD, TH, THead, TR, Table, TableEmpty } from "@/components/ui/table";
import { BoolChip } from "@/components/ops/chips";
import { ConfigSection, CardSubsection } from "@/components/ops/section";
import { DefinitionList } from "@/components/ops/definition-list";
import { HarnessTable } from "@/components/ops/harness-table";
import { LimitsGrid } from "@/components/ops/limits-grid";
import { Notice } from "@/components/ops/notice";
import { StatStrip } from "@/components/ops/stats";
import { VerificationProfiles } from "@/components/ops/verification-profiles";
import {
  asArray,
  asBoolean,
  asNumber,
  asObject,
  dateTimeOr,
  textOr,
  timeAgoOr,
} from "@/components/ops/data";
import {
  formatDuration,
  formatNumber,
  formatPercent,
  timeAgo,
  titleCase,
} from "@/lib/format";
import { hrefFor } from "@/routes";
import { useApi } from "@/lib/hooks";
import { useWorkspace } from "@/lib/workspace";

function RunLink({ runID }) {
  const value = textOr(runID, "");
  if (!value) return <span className="text-muted-foreground">unattributed</span>;
  return (
    <a
      href={hrefFor("run", value)}
      title={value}
      className="mono font-medium text-foreground underline-offset-4 hover:text-primary hover:underline"
    >
      {value}
    </a>
  );
}

function probeState(state) {
  const value = String(state || "").toLowerCase();
  if (!value) return { variant: "muted", label: "Not reported" };
  if (value === "reachable" || value === "ok" || value === "up") return { variant: "success", label: titleCase(value) };
  if (["unreachable", "failed", "error", "timeout", "refused"].includes(value)) {
    return { variant: "danger", label: titleCase(value) };
  }
  return { variant: "warning", label: titleCase(value) };
}

function sandboxState(state) {
  const value = String(state || "").toLowerCase();
  if (!value) return { variant: "muted", label: "Unknown" };
  if (["running", "active"].includes(value)) return { variant: "success", label: titleCase(value) };
  if (["paused", "suspended"].includes(value)) return { variant: "warning", label: titleCase(value) };
  if (["error", "failed"].includes(value)) return { variant: "danger", label: titleCase(value) };
  return { variant: "muted", label: titleCase(value) };
}

function ProbesTable({ probes }) {
  const rows = asArray(probes);
  return (
    <Table minWidth={760} aria-label="Infrastructure reachability probes">
      <THead>
        <TR>
          <TH>Probe</TH>
          <TH>Target</TH>
          <TH>State</TH>
          <TH numeric>Latency</TH>
          <TH>Checked</TH>
        </TR>
      </THead>
      <tbody>
        {rows.length ? (
          rows.map((probe, index) => {
            const state = probeState(probe?.state);
            const latency = asNumber(probe?.latency_ms);
            return (
              <TR key={probe?.id || index}>
                <TD>
                  <div className="font-medium text-foreground">{textOr(probe?.label, textOr(probe?.id, "Probe"))}</div>
                  <div className="mono text-[11px] text-muted-foreground">{textOr(probe?.id)}</div>
                  {probe?.detail ? (
                    <p className="mt-0.5 max-w-[280px] break-words text-[11px] leading-snug text-muted-foreground">
                      {probe.detail}
                    </p>
                  ) : null}
                </TD>
                <TD>
                  <span className="mono text-muted-foreground">{textOr(probe?.target)}</span>
                </TD>
                <TD>
                  <Badge variant={state.variant} size="xs" dot>
                    {state.label}
                  </Badge>
                </TD>
                <TD numeric>{latency === null ? "—" : formatDuration(latency)}</TD>
                <TD>
                  <span className="text-muted-foreground" title={dateTimeOr(probe?.checked_at)}>
                    {timeAgoOr(probe?.checked_at)}
                  </span>
                </TD>
              </TR>
            );
          })
        ) : (
          <TableEmpty
            colSpan={5}
            title="No probes reported"
            description="No reachability probe results were returned in this response."
          />
        )}
      </tbody>
    </Table>
  );
}

function CleanupFailuresTable({ failures, failed, runsWithSandbox }) {
  const rows = asArray(failures);
  const emptyDescription =
    failed !== null && failed > 0
      ? "Failure rows were not included in this response; use the aggregate counts above."
      : runsWithSandbox === 0
        ? "No run in the evidence store recorded a sandbox, so there was no cleanup to verify."
        : "Every recorded run with a sandbox reported a verified cleanup.";
  return (
    <Table minWidth={1020} aria-label="Recent sandbox cleanup failures">
      <THead>
        <TR>
          <TH>Run</TH>
          <TH>Sandbox</TH>
          <TH>Result</TH>
          <TH>Cleanup</TH>
          <TH>Verified</TH>
          <TH numeric>Remaining</TH>
          <TH>Finished</TH>
          <TH>Repository</TH>
          <TH>Error</TH>
        </TR>
      </THead>
      <tbody>
        {rows.length ? (
          rows.map((failure, index) => (
            <TR key={failure?.run_id || index}>
              <TD>
                <RunLink runID={failure?.run_id} />
              </TD>
              <TD>
                <span className="mono text-muted-foreground" title={textOr(failure?.sandbox_id, "")}>
                  {textOr(failure?.sandbox_id)}
                </span>
              </TD>
              <TD>
                <StatusPill kind="run" state={failure?.factory_result} size="xs" />
              </TD>
              <TD>
                <StatusPill
                  kind="cleanup"
                  state={{ outcome: failure?.outcome, verified: failure?.verified }}
                  size="xs"
                />
              </TD>
              <TD>
                <BoolChip
                  value={asBoolean(failure?.verified)}
                  onLabel="Verified"
                  offLabel="Unverified"
                  toneOn="success"
                  toneOff="danger"
                  size="xs"
                />
              </TD>
              <TD numeric>
                <span
                  className={clsx(
                    "tabular font-semibold",
                    asNumber(failure?.remaining_sandboxes) > 0 ? "text-danger" : "text-muted-foreground",
                  )}
                >
                  {formatNumber(failure?.remaining_sandboxes, { fallback: "—" })}
                </span>
              </TD>
              <TD>
                <span className="text-muted-foreground" title={dateTimeOr(failure?.finished_at)}>
                  {timeAgoOr(failure?.finished_at)}
                </span>
              </TD>
              <TD>
                <span className="block max-w-[220px] truncate text-muted-foreground" title={textOr(failure?.repository, "")}>
                  {textOr(failure?.repository)}
                </span>
              </TD>
              <TD>
                {failure?.error ? (
                  <span className="block max-w-[320px] truncate text-danger" title={failure.error}>
                    {failure.error}
                  </span>
                ) : (
                  <span className="text-muted-foreground">—</span>
                )}
              </TD>
            </TR>
          ))
        ) : (
          <TableEmpty
            colSpan={9}
            title={failed !== null && failed > 0 ? "Failure detail not returned" : "No cleanup failures"}
            description={emptyDescription}
          />
        )}
      </tbody>
    </Table>
  );
}

function LiveSandboxesTable({ sandboxes, attributionAvailable = true }) {
  const rows = asArray(sandboxes);
  return (
    <Table minWidth={980} aria-label="Live sandbox inventory">
      <THead>
        <TR>
          <TH>Sandbox</TH>
          <TH>Template</TH>
          <TH>State</TH>
          <TH>Started</TH>
          <TH>Run</TH>
          <TH>Ownership</TH>
          <TH>Flags</TH>
        </TR>
      </THead>
      <tbody>
        {rows.length ? (
          rows.map((sandbox, index) => {
            const state = sandboxState(sandbox?.state);
            const attributionKnown = attributionAvailable && sandbox?.attribution_known === true;
            const leaked = attributionKnown && sandbox?.leaked === true;
            return (
              <TR
                key={sandbox?.id || index}
                className={clsx(leaked && "bg-danger/5 hover:bg-danger/10")}
              >
                <TD>
                  <div className="mono break-all font-medium text-foreground">{textOr(sandbox?.id)}</div>
                  {sandbox?.workflow_run_id ? (
                    <div className="mono text-[11px] text-muted-foreground" title="Temporal workflow run id">
                      wf {sandbox.workflow_run_id}
                    </div>
                  ) : null}
                </TD>
                <TD>
                  <span className="mono text-muted-foreground">{textOr(sandbox?.template)}</span>
                </TD>
                <TD>
                  <Badge variant={state.variant} size="xs" dot>
                    {state.label}
                  </Badge>
                </TD>
                <TD>
                  <span className="text-muted-foreground" title={dateTimeOr(sandbox?.started_at)}>
                    {timeAgoOr(sandbox?.started_at)}
                  </span>
                </TD>
                <TD>
                  <RunLink runID={sandbox?.run_id} />
                </TD>
                <TD>
                  <BoolChip
                    value={asBoolean(sandbox?.factory_owned)}
                    onLabel="Factory"
                    offLabel="Unattributed"
                    toneOn="info"
                    toneOff="muted"
                    size="xs"
                  />
                </TD>
                <TD>
                  <div className="flex flex-wrap items-center gap-1">
                    {leaked ? (
                      <Badge variant="danger" size="xs" dot>
                        Leaked
                      </Badge>
                    ) : null}
                    {attributionKnown && sandbox?.active === true ? (
                      <Badge variant="info" size="xs" dot>
                        Active
                      </Badge>
                    ) : null}
                    {!attributionKnown ? (
                      <Badge variant="muted" size="xs" title="Workflow attribution unavailable; ownership is recorded but activity cannot be classified">
                        Unattributed
                      </Badge>
                    ) : null}
                    {attributionKnown && !leaked && sandbox?.active !== true ? (
                      <span className="text-muted-foreground">—</span>
                    ) : null}
                  </div>
                </TD>
              </TR>
            );
          })
        ) : (
          <TableEmpty
            colSpan={7}
            title="No sandboxes are running"
            description="The live CubeSandbox query returned an empty inventory."
          />
        )}
      </tbody>
    </Table>
  );
}

function OperationsSkeleton() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-44 rounded-panel" />
      <div className="grid gap-4 lg:grid-cols-2">
        <Skeleton className="h-52 rounded-panel" />
        <Skeleton className="h-52 rounded-panel" />
      </div>
      <Skeleton className="h-56 rounded-panel" />
      <TableSkeleton rows={5} columns={6} />
    </div>
  );
}

export function OperationsPage() {
  const { autoRefresh, refreshNonce } = useWorkspace();
  const api = useApi("/operations", {
    pollMs: autoRefresh ? 30000 : 0,
    deps: [refreshNonce],
  });

  const response = asObject(api.data);
  const hasData = Boolean(api.data);
  const updatedLabel = api.updatedAt ? timeAgo(api.updatedAt) : null;

  const cleanup = asObject(response.cleanup);
  const runsWithSandbox = asNumber(cleanup.runs_with_sandbox);
  const verified = asNumber(cleanup.verified);
  const failed = asNumber(cleanup.failed);
  const remaining = asNumber(cleanup.remaining_total);
  const failures = asArray(cleanup.recent_failures);
  const verifiedRate =
    verified !== null && runsWithSandbox !== null && runsWithSandbox > 0 ? verified / runsWithSandbox : null;

  const live = asObject(response.live_sandboxes);
  const liveAvailable = live.available === true;
  const liveKnown = typeof live.available === "boolean";
  const attributionAvailable = live.attribution_available === true;
  const sandboxes = asArray(live.sandboxes);

  const infrastructure = asObject(response.infrastructure);
  const cube = asObject(infrastructure.cube);
  const temporal = asObject(infrastructure.temporal);
  const observability = asObject(infrastructure.observability);
  const storage = asObject(infrastructure.storage);
  const harnesses = asArray(response.harnesses);
  const profiles = asArray(response.verification_profiles);
  const limits = asObject(response.limits);
  const review = asObject(response.review);
  const hardening = asObject(response.hardening);
  const probes = asArray(response.probes);
  const dataHealth = asObject(response.data_health);
  const hasDataHealth = Object.keys(dataHealth).length > 0;
  const index = asObject(dataHealth.index);
  const notes = asArray(dataHealth.notes);
  const warnings = asArray(response.warnings);

  const indexItems = [
    { label: "Runs indexed", value: formatNumber(index.runs, { fallback: "—" }) },
    { label: "Changes indexed", value: formatNumber(index.changes, { fallback: "—" }) },
    {
      label: "Last refresh",
      value: timeAgoOr(index.refreshed_at),
      hint: dateTimeOr(index.refreshed_at, "Not reported"),
    },
    { label: "Scan duration", value: formatDuration(index.scan_ms) },
    {
      label: "Scan errors",
      value: (
        <span
          className={clsx(
            "tabular font-semibold",
            asNumber(index.scan_errors) > 0 ? "text-warning" : "text-foreground",
          )}
        >
          {formatNumber(index.scan_errors, { fallback: "—" })}
        </span>
      ),
    },
    { label: "Oldest run", value: dateTimeOr(index.oldest_run_at) },
    { label: "Newest run", value: dateTimeOr(index.newest_run_at) },
    { label: "Response generated", value: dateTimeOr(response.generated_at) },
  ];

  return (
    <div className="mx-auto w-full max-w-[1600px] px-4 py-5 sm:px-6 lg:px-8">
      <PageHeader
        eyebrow="Governance · Operations"
        title="Operations"
        description="Sandbox cleanup evidence, live sandbox inventory, infrastructure reachability and the configured execution catalog. Probes measure reachability — they are not health claims, and unavailable live data is never shown as zero."
        meta={
          <>
            {response.environment ? (
              <Badge variant="outline" size="sm">
                {response.environment}
              </Badge>
            ) : null}
            <span>Generated {dateTimeOr(response.generated_at)}</span>
            <span aria-hidden="true" className="text-border-strong">
              ·
            </span>
            <span>Live inventory checked {timeAgoOr(live.checked_at, "not reported")}</span>
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
            {api.error.message || "The operations endpoint could not be reached."} This view may be{" "}
            {updatedLabel || "stale"}.
          </Notice>
        ) : null}

        {api.error && !hasData ? (
          <Notice
            tone="danger"
            title="Operations data could not be loaded"
            announce
            action={
              <Button variant="secondary" size="xs" onClick={api.refresh}>
                Retry
              </Button>
            }
          >
            {api.error.message || "The operations endpoint could not be reached."}
          </Notice>
        ) : null}

        {warnings.length ? (
          <Notice tone="warning" title={`${warnings.length} configuration warning${warnings.length === 1 ? "" : "s"}`}>
            <ul className="list-disc space-y-1 pl-4">
              {warnings.map((warning, index_) => (
                <li key={index_}>{warning}</li>
              ))}
            </ul>
          </Notice>
        ) : null}

        {!hasData && api.loading ? <OperationsSkeleton /> : null}

        {hasData ? (
          <>
            <Card>
              <CardHeader
                icon={<PackageCheck />}
                title="Sandbox cleanup"
                description="Evidence-derived lifecycle outcome for every run that used a sandbox."
                actions={
                  <Badge
                    variant={
                      failed === null ? "outline" : failed > 0 ? "danger" : runsWithSandbox === 0 ? "outline" : "success"
                    }
                    size="sm"
                    dot
                  >
                    {failed === null
                      ? "Failure count not reported"
                      : failed > 0
                        ? `${formatNumber(failed)} failed`
                        : runsWithSandbox === 0
                          ? "No sandbox runs"
                          : "All verified"}
                  </Badge>
                }
              />
              <StatStrip
                columns={4}
                items={[
                  {
                    label: "Runs with sandbox",
                    value: runsWithSandbox,
                    hint: "Runs that recorded a sandbox id",
                  },
                  {
                    label: "Verified clean",
                    value: verified,
                    tone: verified !== null ? "success" : "muted",
                    hint: "Cleanup verified with zero remaining sandboxes",
                  },
                  {
                    label: "Failed",
                    value: failed,
                    tone: failed !== null && failed > 0 ? "danger" : "muted",
                    hint: "Cleanup did not verify",
                  },
                  {
                    label: "Remaining sandboxes",
                    value: remaining,
                    tone: remaining !== null && remaining > 0 ? "danger" : "muted",
                    hint: "Reported leftovers summed across runs",
                  },
                ]}
              />
              <CardBody className="border-t border-border">
                <div className="flex flex-wrap items-end justify-between gap-2">
                  <div>
                    <p className="text-[11px] font-medium text-foreground">Verified cleanup rate</p>
                    <p className="text-[11px] leading-snug text-muted-foreground">
                      Share of sandbox runs whose cleanup was verified clean.
                    </p>
                  </div>
                  <span className="tabular text-[18px] font-semibold leading-none text-foreground">
                    {formatPercent(verifiedRate, { fallback: "—" })}
                  </span>
                </div>
                <div
                  className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted"
                  role="img"
                  aria-label={`Verified cleanup rate: ${formatPercent(verifiedRate, { fallback: "not reported" })}`}
                >
                  <div
                    className={clsx(
                      "h-full rounded-full transition-[width] duration-300",
                      failed !== null && failed > 0 ? "bg-warning" : "bg-success",
                    )}
                    style={{ width: `${Math.round((verifiedRate ?? 0) * 100)}%` }}
                  />
                </div>
              </CardBody>
              <CardSubsection
                title="Recent cleanup failures"
                description="Runs whose sandbox cleanup did not verify"
                actions={
                  <Badge variant={failures.length ? "warning" : "outline"} size="xs">
                    {formatNumber(failures.length)} recorded
                  </Badge>
                }
              >
                <CleanupFailuresTable failures={failures} failed={failed} runsWithSandbox={runsWithSandbox} />
              </CardSubsection>
            </Card>

            <Card>
              <CardHeader
                icon={<Boxes />}
                title="Live sandbox inventory"
                description={
                  attributionAvailable
                    ? "Point-in-time query of the CubeSandbox API. Leaked means a factory-owned sandbox with no active run."
                    : "Point-in-time query of the CubeSandbox API. Leak classification needs live workflow attribution, which is unavailable right now."
                }
                actions={
                  <BoolChip
                    value={liveKnown ? live.available : undefined}
                    onLabel="Query available"
                    offLabel="Query unavailable"
                    toneOn="success"
                    toneOff="warning"
                  />
                }
              />
              {liveAvailable ? (
                <>
                  <StatStrip
                    columns={4}
                    items={[
                      { label: "Total", value: asNumber(live.total), hint: "All sandboxes reported by Cube" },
                      { label: "Factory owned", value: asNumber(live.factory_owned), tone: "info", hint: "Tagged with a factory run" },
                      {
                        label: "Active",
                        value: attributionAvailable ? asNumber(live.active) : "Unknown",
                        tone: attributionAvailable ? "info" : "muted",
                        hint: attributionAvailable ? "Sandboxes attached to a live run" : "Workflow attribution unavailable",
                      },
                      {
                        label: "Leaked",
                        value: attributionAvailable ? asNumber(live.leaked) : "Unknown",
                        tone: attributionAvailable && asNumber(live.leaked) > 0 ? "danger" : "muted",
                        hint: attributionAvailable
                          ? "Factory-owned sandboxes without an active run"
                          : "Withheld until workflow attribution is available",
                      },
                    ]}
                  />
                  {!attributionAvailable ? (
                    <div className="px-4 pb-3">
                      <Notice tone="warning" title="Leak classification withheld" icon={<CircleAlert />}>
                        {live.note ||
                          "The console could not read open-workflow state, so a factory-owned sandbox cannot be called a leak. Sandboxes are listed with ownership only."}
                      </Notice>
                    </div>
                  ) : null}
                  <LiveSandboxesTable sandboxes={sandboxes} attributionAvailable={attributionAvailable} />
                </>
              ) : (
                <CardBody>
                  <Notice
                    tone="warning"
                    title="Live sandbox inventory unavailable"
                    icon={<CircleAlert />}
                  >
                    {live.error ? (
                      <>
                        The CubeSandbox query failed: <span className="mono break-all">{live.error}</span>
                      </>
                    ) : liveKnown ? (
                      "The console could not query CubeSandbox. Totals are withheld rather than reported as zero."
                    ) : (
                      "This response did not report live inventory availability. Totals are withheld rather than reported as zero."
                    )}
                  </Notice>
                </CardBody>
              )}
              <CardFooter>
                Live CubeSandbox query, bounded and cached by the console · checked{" "}
                {timeAgoOr(live.checked_at, "time not reported")}
                {attributionAvailable ? "" : " · workflow attribution unavailable"}
              </CardFooter>
            </Card>

            <div className="grid gap-4 lg:grid-cols-2">
              <ConfigSection
                icon={<Server />}
                title="CubeSandbox"
                description="Redacted sandbox provider configuration."
              >
                <DefinitionList
                  columns={2}
                  items={[
                    { label: "API URL", value: cube.api_url, mono: true },
                    { label: "Template", value: cube.template_id, mono: true },
                    { label: "Proxy", value: cube.proxy, mono: true },
                    { label: "Version", value: cube.version },
                    {
                      label: "Credentials",
                      value: (
                        <BoolChip
                          value={asBoolean(cube.credentials_configured)}
                          onLabel="Configured"
                          offLabel="Not configured"
                          toneOn="success"
                          toneOff="muted"
                        />
                      ),
                      hint: "Only presence is reported; credential values are never returned.",
                    },
                  ]}
                />
              </ConfigSection>

              <ConfigSection
                icon={<Workflow />}
                title="Temporal"
                description="Workflow orchestration endpoint and namespace."
              >
                <DefinitionList
                  columns={2}
                  items={[
                    { label: "Host:port", value: temporal.host_port, mono: true },
                    { label: "Namespace", value: temporal.namespace, mono: true },
                    { label: "Task queue", value: temporal.task_queue, mono: true },
                    {
                      label: "TLS",
                      value: (
                        <BoolChip
                          value={asBoolean(temporal.tls)}
                          onLabel="Enabled"
                          offLabel="Disabled"
                          toneOn="success"
                          toneOff="warning"
                        />
                      ),
                    },
                    {
                      label: "API key env",
                      value: temporal.api_key_env,
                      mono: true,
                      hint: "Environment variable name only; the value is never returned.",
                    },
                    {
                      label: "Payload keyring",
                      value: (
                        <BoolChip
                          value={asBoolean(temporal.payload_keyring_configured)}
                          onLabel="Configured"
                          offLabel="Not configured"
                          toneOn="success"
                          toneOff="muted"
                        />
                      ),
                    },
                  ]}
                />
              </ConfigSection>

              <ConfigSection
                icon={<Activity />}
                title="Observability"
                description="OpenTelemetry export configuration."
              >
                <DefinitionList
                  columns={2}
                  items={[
                    {
                      label: "OTLP export",
                      value: (
                        <BoolChip
                          value={asBoolean(observability.otel_configured)}
                          onLabel="Configured"
                          offLabel="Not configured"
                          toneOn="success"
                          toneOff="warning"
                        />
                      ),
                      hint: "When not configured, telemetry stays local to the factory.",
                    },
                    { label: "Service name", value: observability.service_name, mono: true },
                  ]}
                />
              </ConfigSection>

              <ConfigSection
                icon={<Database />}
                title="Evidence storage"
                description="Durable artifact store backing every console number."
              >
                <DefinitionList
                  columns={2}
                  items={[
                    { label: "Data directory", value: storage.data_dir, mono: true, span: "full" },
                    { label: "Runs on disk", value: formatNumber(storage.run_count, { fallback: "—" }) },
                    { label: "Changes on disk", value: formatNumber(storage.change_count, { fallback: "—" }) },
                  ]}
                />
              </ConfigSection>
            </div>

            <Card>
              <CardHeader
                icon={<Radar />}
                title="Reachability probes"
                description="Bounded TCP probes to configured dependencies. A successful handshake is reachability, not a health claim."
                actions={
                  <Badge variant="outline" size="sm">
                    {formatNumber(probes.length)} probe{probes.length === 1 ? "" : "s"}
                  </Badge>
                }
              />
              <ProbesTable probes={probes} />
              <CardFooter>Executed at request time · {dateTimeOr(response.generated_at)}</CardFooter>
            </Card>

            <Card>
              <CardHeader
                icon={<Layers />}
                title="Agent harnesses"
                description="Configured agent adapters, their pinning posture and the environment variable names they receive."
                actions={
                  <Badge variant="outline" size="sm">
                    {formatNumber(harnesses.length)} configured
                  </Badge>
                }
              />
              <HarnessTable harnesses={harnesses} />
              <CardFooter>
                Environment variable values are never returned; only names are shown.
              </CardFooter>
            </Card>

            <Card>
              <CardHeader
                icon={<ListChecks />}
                title="Verification gates"
                description="Deterministic gate steps executed inside the sandbox after the agent."
                actions={
                  <Badge variant="outline" size="sm">
                    {formatNumber(profiles.length)} profile{profiles.length === 1 ? "" : "s"}
                  </Badge>
                }
              />
              <CardBody>
                <VerificationProfiles profiles={profiles} />
              </CardBody>
              <CardFooter>Mandatory steps must pass before a change is accepted.</CardFooter>
            </Card>

            <ConfigSection
              icon={<Gauge />}
              title="Execution limits"
              description="Effective worker and sandbox limits applied by this factory."
            >
              <LimitsGrid limits={limits} />
            </ConfigSection>

            <div className="grid gap-4 xl:grid-cols-2">
              <ConfigSection
                icon={<ShieldCheck />}
                title="Review gate"
                description="Configured human review behaviour."
              >
                {review.enabled === false ? (
                  <Notice tone="warning" title="Review gate is disabled" className="mb-3">
                    Runs will not pause for human approval, and no review decision can be recorded.
                  </Notice>
                ) : null}
                <DefinitionList
                  columns={1}
                  items={[
                    {
                      label: "Enabled",
                      value: (
                        <BoolChip
                          value={asBoolean(review.enabled)}
                          onLabel="Enabled"
                          offLabel="Disabled"
                          toneOn="success"
                          toneOff="warning"
                        />
                      ),
                    },
                    { label: "Decision timeout", value: formatDuration(review.timeout_ms) },
                    {
                      label: "Suspend sandbox while paused",
                      value: (
                        <BoolChip
                          value={asBoolean(review.suspend)}
                          onLabel="Checkpointed"
                          offLabel="Kept live"
                          toneOn="info"
                          toneOff="muted"
                        />
                      ),
                      hint: "Checkpointing is honoured only when the sandbox provider supports suspend.",
                    },
                  ]}
                />
              </ConfigSection>

              <ConfigSection
                icon={<ShieldCheck />}
                title="Hardening posture"
                description="Per-run sandbox and behavior monitoring configuration."
              >
                <DefinitionList
                  columns={1}
                  items={[
                    {
                      label: "Egress probing",
                      value: (
                        <BoolChip
                          value={asBoolean(hardening.egress_probe_enabled)}
                          onLabel="Enabled"
                          offLabel="Disabled"
                          toneOn="success"
                          toneOff="warning"
                        />
                      ),
                    },
                    {
                      label: "Behavior monitoring",
                      value: (
                        <BoolChip
                          value={asBoolean(hardening.behavior_monitor_enabled)}
                          onLabel="Enabled"
                          offLabel="Disabled"
                          toneOn="success"
                          toneOff="warning"
                        />
                      ),
                    },
                    {
                      label: "Trip severity",
                      value: hardening.trip_severity ? (
                        <StatusPill kind="severity" state={hardening.trip_severity} size="xs" />
                      ) : undefined,
                    },
                    {
                      label: "Require non-root",
                      value: (
                        <BoolChip
                          value={asBoolean(hardening.require_non_root)}
                          onLabel="Enforced"
                          offLabel="Not enforced"
                          toneOn="success"
                          toneOff="warning"
                        />
                      ),
                    },
                    {
                      label: "Open egress acknowledged",
                      value: (
                        <BoolChip
                          value={asBoolean(hardening.acknowledge_open_egress)}
                          onLabel="Acknowledged"
                          offLabel="Strict"
                          toneOn="warning"
                          toneOff="success"
                        />
                      ),
                    },
                    {
                      label: "Gate self-modification",
                      value: (
                        <BoolChip
                          value={asBoolean(hardening.allow_gate_self_modification)}
                          onLabel="Allowed"
                          offLabel="Blocked"
                          toneOn="danger"
                          toneOff="success"
                        />
                      ),
                    },
                    {
                      label: "Alert webhook",
                      value: (
                        <BoolChip
                          value={asBoolean(hardening.alert_webhook_configured)}
                          onLabel="Configured"
                          offLabel="Not configured"
                          toneOn="success"
                          toneOff="danger"
                        />
                      ),
                    },
                    { label: "Alert timeout", value: formatDuration(hardening.alert_timeout_ms) },
                    { label: "Egress canary", value: hardening.egress_canary_url, mono: true, span: "full" },
                    { label: "Metadata probe", value: hardening.metadata_probe_url, mono: true, span: "full" },
                  ]}
                />
              </ConfigSection>
            </div>

            <Card>
              <CardHeader
                icon={<Database />}
                title="Evidence data health"
                description="Readability and freshness of the artifact index that backs every operations number."
                actions={
                  <BoolChip
                    value={asBoolean(dataHealth.storage_readable)}
                    onLabel="Store readable"
                    offLabel="Store unreadable"
                    toneOn="success"
                    toneOff="danger"
                  />
                }
              />
              <CardBody className="space-y-4">
                {!hasDataHealth ? (
                  <Notice tone="warning" title="Data health not reported">
                    This response did not include a data-health block, so storage readability and index freshness are
                    not claimed.
                  </Notice>
                ) : null}
                {dataHealth.storage_error ? (
                  <Notice tone="danger" title="Storage read error">
                    <span className="mono break-all">{dataHealth.storage_error}</span>
                  </Notice>
                ) : null}
                <DefinitionList items={indexItems} columns={4} />
                {notes.length ? (
                  <div className="border-t border-border pt-3">
                    <p className="eyebrow mb-1.5">Data notes</p>
                    <ul className="space-y-1 text-[11px] leading-relaxed text-muted-foreground">
                      {notes.map((note, index_) => (
                        <li key={index_} className="flex gap-1.5">
                          <span aria-hidden="true" className="text-border-strong">
                            –
                          </span>
                          <span className="break-words">{note}</span>
                        </li>
                      ))}
                    </ul>
                  </div>
                ) : (
                  <p className="border-t border-border pt-3 text-[11px] text-muted-foreground">
                    No data-health notes were reported.
                  </p>
                )}
              </CardBody>
              <CardFooter>
                {hasDataHealth
                  ? `Index refreshed ${timeAgoOr(index.refreshed_at)} · scan ${formatDuration(index.scan_ms)} · ${formatNumber(
                      index.scan_errors,
                      { fallback: "—" },
                    )} scan errors`
                  : "Data-health block not returned in this response."}
              </CardFooter>
            </Card>
          </>
        ) : null}
      </div>
    </div>
  );
}
