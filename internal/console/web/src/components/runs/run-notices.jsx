import { AlertTriangle, Info, Radio, Wallet } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { formatDateTime, formatDuration, repoLabel, shortSha } from "@/lib/format";
import { changeHref } from "./utils";

// RunNotices renders the evidence-integrity messages the API returns with a
// run. Warnings are not cosmetic: they explain when a section is missing.
export function RunNotices({ warnings = [], budgetExceeded = [] }) {
  const hasWarnings = Array.isArray(warnings) && warnings.length > 0;
  const hasBudget = Array.isArray(budgetExceeded) && budgetExceeded.length > 0;
  if (!hasWarnings && !hasBudget) return null;

  return (
    <div className="mb-4 space-y-2">
      {hasWarnings ? (
        <Card className="border-warning/30 bg-warning/5">
          <div className="flex items-start gap-2.5 px-4 py-3 sm:px-5">
            <AlertTriangle aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0 text-warning" />
            <div className="min-w-0 flex-1">
              <h2 className="text-[12px] font-semibold text-warning">Evidence warnings</h2>
              <ul className="mt-1 space-y-1 text-[12px] leading-relaxed text-muted-foreground">
                {warnings.map((warning) => (
                  <li key={warning} className="break-words">
                    {warning}
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </Card>
      ) : null}
      {hasBudget ? (
        <Card className="border-warning/30 bg-warning/5">
          <div className="flex items-start gap-2.5 px-4 py-3 sm:px-5">
            <Wallet aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0 text-warning" />
            <div className="min-w-0 flex-1">
              <h2 className="text-[12px] font-semibold text-warning">Budget limits exceeded</h2>
              <ul className="mt-1 space-y-1 text-[12px] leading-relaxed text-muted-foreground">
                {budgetExceeded.map((item) => (
                  <li key={item} className="break-words">
                    {item}
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </Card>
      ) : null}
    </div>
  );
}

// LiveRunBanner explains why the manifest sections are empty for a run that is
// still executing: there is live orchestration state, but no durable evidence
// has been written yet. Nothing is fabricated while waiting.
export function LiveRunBanner({ live, run }) {
  if (!live) return null;
  const rows = [
    ["Workflow", live.workflow_id ? <Mono key="wf" value={live.workflow_id} /> : "—"],
    [
      "State",
      <Badge key="state" variant="info" dot>
        {String(live.state || "Unknown").replace(/_/g, " ")}
      </Badge>,
    ],
    ["Current step", live.current_step || "—"],
    ["Started", <Time key="started" value={live.started_at} />],
    [
      "Sandbox",
      live.sandbox_id ? (
        <span key="sandbox" className="inline-flex flex-wrap items-center gap-1.5">
          <Mono value={live.sandbox_id} />
          {live.sandbox_destroyed ? <Badge variant="muted" size="xs">destroyed</Badge> : null}
        </span>
      ) : (
        "—"
      ),
    ],
    ["Repository", run?.repository ? <span key="repo" title={run.repository}>{repoLabel(run.repository)}</span> : "—"],
    ["Harness", run?.agent_harness || "—"],
    [
      "Change",
      live.change_id ? (
        <a key="change" href={changeHref(live.change_id)} className="font-mono text-primary hover:underline">
          {shortSha(live.change_id, 12)}
        </a>
      ) : (
        "—"
      ),
    ],
  ];

  return (
    <Card className="mb-4 border-info/30 bg-info/5">
      <div className="flex flex-wrap items-start gap-3 px-4 py-3 sm:px-5">
        <span className="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-info/10 text-info [&>svg]:h-4 [&>svg]:w-4">
          <Radio aria-hidden="true" className="animate-pulse" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-[13px] font-semibold text-foreground">Live orchestration state</h2>
            <Badge variant="info" dot>
              No durable manifest yet
            </Badge>
          </div>
          <p className="mt-1 max-w-3xl text-[12px] leading-relaxed text-muted-foreground">
            This run is still executing, so its manifest, verification result, patch and artifact list are not written
            yet. The fields below are queried live from Temporal and are not a completed-run result.
          </p>
          <dl className="mt-3 grid grid-cols-1 gap-x-6 gap-y-2 sm:grid-cols-2 lg:grid-cols-4">
            {rows.map(([label, value]) => (
              <div key={label} className="min-w-0">
                <dt className="text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">{label}</dt>
                <dd className="mt-0.5 break-words text-[12px] leading-snug text-foreground">{value}</dd>
              </div>
            ))}
          </dl>
          {live.query_error ? (
            <p className="mt-2 text-[11px] text-warning">Live query error: {live.query_error}</p>
          ) : null}
        </div>
      </div>
    </Card>
  );
}

export function NoManifestNotice() {
  return (
    <Card className="mb-4 border-warning/30 bg-warning/5">
      <div className="flex items-start gap-2.5 px-4 py-3 sm:px-5">
        <Info aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0 text-warning" />
        <div className="min-w-0 flex-1">
          <h2 className="text-[12px] font-semibold text-warning">No durable manifest recorded</h2>
          <p className="mt-1 text-[12px] leading-relaxed text-muted-foreground">
            The console could not find a run manifest for this ID. Sections that depend on it are shown as unavailable
            rather than inferred from partial evidence.
          </p>
        </div>
      </div>
    </Card>
  );
}

function Mono({ value }) {
  return (
    <span className="break-all font-mono text-[11px] leading-relaxed text-foreground" title={value}>
      {value}
    </span>
  );
}

function Time({ value }) {
  if (!value) return "—";
  return (
    <span title={value}>
      {formatDuration(Date.now() - new Date(value).getTime())} ago
      <span className="ml-1 text-muted-foreground">· {formatDateTime(value)}</span>
    </span>
  );
}

