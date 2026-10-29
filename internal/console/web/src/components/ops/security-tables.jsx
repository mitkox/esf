import clsx from "clsx";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusPill } from "@/components/ui/status-pill";
import { TD, TH, THead, TR, Table, TableEmpty } from "@/components/ui/table";
import {
  formatDateTime,
  formatDuration,
  formatNumber,
  repoLabel,
} from "@/lib/format";
import { hrefFor } from "@/routes";
import { BoolChip, ReachChip } from "./chips";
import { asArray, asNumber, dateTimeOr, textOr, timeAgoOr } from "./data";

function RunLink({ runID }) {
  const value = textOr(runID, "");
  if (!value) return <span className="text-muted-foreground">—</span>;
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

function NumericTone({ value, danger = true }) {
  const number = asNumber(value);
  if (number === null) return <span className="text-muted-foreground">—</span>;
  return (
    <span
      className={clsx(
        "tabular font-semibold",
        danger && number > 0 ? "text-danger" : number > 0 ? "text-foreground" : "text-muted-foreground",
      )}
    >
      {formatNumber(number)}
    </span>
  );
}

// FindingsTable is the triage list: only runs with a security signal arrive
// here, newest first, with the exact evidence flags an operator acts on.
export function FindingsTable({ entries = [], emptyDescription }) {
  const rows = asArray(entries);
  return (
    <Table minWidth={1120} aria-label="Runs with security signals">
      <THead>
        <TR>
          <TH>Run</TH>
          <TH>Result</TH>
          <TH>Repository</TH>
          <TH>Harness</TH>
          <TH>Scope</TH>
          <TH numeric>Findings</TH>
          <TH>Max severity</TH>
          <TH>Tripped</TH>
          <TH>Gate modified</TH>
          <TH numeric>Egress issues</TH>
          <TH>Finished</TH>
        </TR>
      </THead>
      <tbody>
        {rows.length ? (
          rows.map((entry, index) => {
            const findings = asNumber(entry?.findings);
            const gateModified = asArray(entry?.gate_modified);
            return (
              <TR key={entry?.run_id || index}>
                <TD>
                  <RunLink runID={entry?.run_id} />
                </TD>
                <TD>
                  <StatusPill kind="run" state={entry?.result} size="xs" />
                </TD>
                <TD>
                  <span
                    className="block max-w-[220px] truncate text-muted-foreground"
                    title={textOr(entry?.repository, "")}
                  >
                    {repoLabel(entry?.repository)}
                  </span>
                </TD>
                <TD muted>
                  <span className="mono">{textOr(entry?.agent_harness)}</span>
                </TD>
                <TD muted>{textOr(entry?.scope)}</TD>
                <TD numeric>
                  <span
                    className={clsx(
                      "tabular font-semibold",
                      findings !== null && findings > 0 ? "text-danger" : "text-muted-foreground",
                    )}
                  >
                    {findings === null ? "—" : formatNumber(findings)}
                  </span>
                </TD>
                <TD>
                  {entry?.max_severity ? (
                    <StatusPill kind="severity" state={entry.max_severity} size="xs" />
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TD>
                <TD>
                  {typeof entry?.tripped === "boolean" ? (
                    <BoolChip
                      value={entry.tripped}
                      onLabel="Tripped"
                      offLabel="No"
                      toneOn="danger"
                      toneOff="muted"
                      size="xs"
                    />
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TD>
                <TD>
                  {gateModified.length ? (
                    <Badge
                      variant="danger"
                      size="xs"
                      title={gateModified.join("\n")}
                    >
                      {gateModified.length} file{gateModified.length === 1 ? "" : "s"}
                    </Badge>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TD>
                <TD numeric>
                  <NumericTone value={entry?.egress_issues} />
                </TD>
                <TD>
                  <span className="text-muted-foreground" title={dateTimeOr(entry?.finished_at)}>
                    {timeAgoOr(entry?.finished_at)}
                  </span>
                </TD>
              </TR>
            );
          })
        ) : (
          <TableEmpty
            colSpan={11}
            title="No runs with security signals"
            description={
              emptyDescription ||
              "No run in this window produced a finding, security stop, gate modification or egress issue."
            }
          />
        )}
      </tbody>
    </Table>
  );
}

export function RulesTable({ rules = [] }) {
  const rows = asArray(rules);
  return (
    <Table minWidth={560} aria-label="Behavior rules aggregated across runs">
      <THead>
        <TR>
          <TH>Rule</TH>
          <TH>Category</TH>
          <TH>Severity</TH>
          <TH numeric>Runs</TH>
          <TH numeric>Findings</TH>
        </TR>
      </THead>
      <tbody>
        {rows.length ? (
          rows.map((rule, index) => {
            const findings = asNumber(rule?.findings);
            return (
              <TR key={rule?.rule_id || index}>
                <TD>
                  <span className="mono break-all text-foreground">{textOr(rule?.rule_id)}</span>
                </TD>
                <TD muted>{textOr(rule?.category)}</TD>
                <TD>
                  {rule?.severity ? (
                    <StatusPill kind="severity" state={rule.severity} size="xs" />
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TD>
                <TD numeric>{formatNumber(rule?.runs, { fallback: "—" })}</TD>
                <TD numeric>
                  <span className={clsx("tabular font-semibold", findings !== null && findings > 0 ? "text-danger" : "text-muted-foreground")}>
                    {findings === null ? "—" : formatNumber(findings)}
                  </span>
                </TD>
              </TR>
            );
          })
        ) : (
          <TableEmpty
            colSpan={5}
            title="No behavior findings"
            description="No hardening rule fired across the inspected runs in this window."
          />
        )}
      </tbody>
    </Table>
  );
}

export function EgressObservationsTable({ observations = [] }) {
  const rows = asArray(observations);
  return (
    <Table minWidth={920} aria-label="Per-run egress probe observations">
      <THead>
        <TR>
          <TH>Run</TH>
          <TH>Policy</TH>
          <TH>Expect deny</TH>
          <TH>Canary</TH>
          <TH>Metadata endpoint</TH>
          <TH>Agent UID</TH>
          <TH numeric>Violations</TH>
        </TR>
      </THead>
      <tbody>
        {rows.length ? (
          rows.map((observation, index) => {
            const uid = asNumber(observation?.agent_uid);
            const expectDeny = observation?.expect_deny === true;
            return (
              <TR key={observation?.run_id || index}>
                <TD>
                  <RunLink runID={observation?.run_id} />
                </TD>
                <TD>
                  <span className="mono text-muted-foreground">{textOr(observation?.policy)}</span>
                </TD>
                <TD>
                  <BoolChip
                    value={typeof observation?.expect_deny === "boolean" ? observation.expect_deny : undefined}
                    onLabel="Deny expected"
                    offLabel="Allowed by policy"
                    toneOn="warning"
                    toneOff="muted"
                    size="xs"
                  />
                </TD>
                <TD>
                  <ReachChip
                    decided={observation?.canary_decided}
                    reachable={observation?.canary_reachable}
                    toneReachable={expectDeny ? "danger" : "info"}
                    toneBlocked="success"
                    size="xs"
                    title="Canary reachability measured inside the sandbox"
                  />
                </TD>
                <TD>
                  <ReachChip
                    decided={observation?.metadata_decided}
                    reachable={observation?.metadata_reachable}
                    toneReachable="danger"
                    toneBlocked="success"
                    size="xs"
                    title="Cloud metadata endpoint reachability"
                  />
                </TD>
                <TD>
                  {uid === null ? (
                    <span className="text-muted-foreground">—</span>
                  ) : uid === 0 ? (
                    <Badge variant="warning" size="xs" title="Agent ran with root uid inside the sandbox">
                      uid 0 (root)
                    </Badge>
                  ) : (
                    <span className="mono text-muted-foreground">uid {uid}</span>
                  )}
                </TD>
                <TD numeric>
                  <NumericTone value={observation?.violations} />
                </TD>
              </TR>
            );
          })
        ) : (
          <TableEmpty
            colSpan={7}
            title="No egress observations"
            description="No run in this window recorded an egress probe result."
          />
        )}
      </tbody>
    </Table>
  );
}

export function AlertsList({ alerts = [] }) {
  const rows = asArray(alerts);
  if (!rows.length) {
    return (
      <EmptyState
        compact
        title="No security alerts"
        description="No alert was recorded from run evidence in this window."
      />
    );
  }
  return (
    <ul className="divide-y divide-border">
      {rows.map((alert, index) => (
        <li key={`${alert?.run_id || "alert"}:${alert?.kind || ""}:${alert?.at || index}`} className="px-4 py-3 sm:px-5">
          <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-1.5">
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-1.5">
                <StatusPill kind="severity" state={alert?.severity} size="xs" />
                <span className="mono text-[11px] text-muted-foreground">{textOr(alert?.kind)}</span>
                <BoolChip
                  value={typeof alert?.delivered === "boolean" ? alert.delivered : undefined}
                  onLabel="Delivered"
                  offLabel="Not delivered"
                  toneOn="success"
                  toneOff="danger"
                  size="xs"
                />
              </div>
              <p className="mt-1 text-xs leading-relaxed text-foreground">
                {textOr(alert?.summary, "No summary recorded.")}
              </p>
              {alert?.error ? (
                <p className="mt-0.5 break-words text-[11px] leading-snug text-danger">
                  Delivery error: {alert.error}
                </p>
              ) : null}
            </div>
            <div className="shrink-0 text-right">
              <RunLink runID={alert?.run_id} />
              <p className="mt-0.5 text-[11px] text-muted-foreground" title={dateTimeOr(alert?.at)}>
                {timeAgoOr(alert?.at, "Time not recorded")}
              </p>
            </div>
          </div>
        </li>
      ))}
    </ul>
  );
}

export function StopsTable({ stops = [] }) {
  const rows = asArray(stops);
  return (
    <Table minWidth={780} aria-label="Runs stopped for security reasons">
      <THead>
        <TR>
          <TH>Run</TH>
          <TH>Result</TH>
          <TH>Harness</TH>
          <TH>Repository</TH>
          <TH numeric>Duration</TH>
          <TH>Completed</TH>
          <TH>Cleanup</TH>
        </TR>
      </THead>
      <tbody>
        {rows.length ? (
          rows.map((stop, index) => (
            <TR key={stop?.run_id || index}>
              <TD>
                <RunLink runID={stop?.run_id} />
              </TD>
              <TD>
                <StatusPill kind="run" state={stop?.factory_result} size="xs" />
              </TD>
              <TD>
                <span className="mono text-muted-foreground">{textOr(stop?.agent_harness)}</span>
              </TD>
              <TD>
                <span className="block max-w-[200px] truncate text-muted-foreground" title={textOr(stop?.repository, "")}>
                  {repoLabel(stop?.repository)}
                </span>
              </TD>
              <TD numeric>{formatDuration(stop?.duration_ms)}</TD>
              <TD>
                <span className="text-muted-foreground" title={formatDateTime(stop?.completed_at)}>
                  {timeAgoOr(stop?.completed_at)}
                </span>
              </TD>
              <TD>
                <StatusPill kind="cleanup" state={stop?.cleanup_result} size="xs" />
              </TD>
            </TR>
          ))
        ) : (
          <TableEmpty
            colSpan={7}
            title="No security stops"
            description="No run in this window was quarantined, tampered with or stopped for unverified egress."
          />
        )}
      </tbody>
    </Table>
  );
}
