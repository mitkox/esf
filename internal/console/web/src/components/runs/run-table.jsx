import clsx from "clsx";
import { ChevronRight } from "lucide-react";
import { Table, THead, TH, TR, TD } from "@/components/ui/table";
import { StatusPill } from "@/components/ui/status-pill";
import { formatDate, formatDuration, formatDateTime, repoLabel, timeAgo } from "@/lib/format";
import { CleanupIndicator, ResultSplit, ReviewIndicator, SecurityIndicator } from "./signals";
import { runHref } from "./utils";

function RunRow({ run }) {
  const href = runHref(run.run_id);
  const open = () => {
    if (window.location.hash !== href) window.location.hash = href;
  };
  const onRowClick = (event) => {
    // Nested links and controls keep their own behaviour; the rest of the row
    // behaves as a link without trapping keyboard users (the ID anchor covers
    // keyboard navigation).
    if (event.target.closest("a, button, input, select, label, textarea")) return;
    open();
  };

  return (
    <TR onClick={onRowClick} className="cursor-pointer">
      <TD className="max-w-[340px]">
        <a
          href={href}
          title={run.run_id}
          className="block max-w-full truncate font-mono text-[12px] font-medium leading-5 text-foreground transition-colors hover:text-primary"
        >
          {run.run_id}
        </a>
        <div className="mt-0.5 flex min-w-0 items-center gap-1 text-[10.5px] leading-4 text-muted-foreground">
          <span className="truncate" title={run.repository}>
            {repoLabel(run.repository)}
          </span>
          {run.repository_kind ? <span className="shrink-0 opacity-60">· {run.repository_kind}</span> : null}
          {run.change_id ? (
            <a
              href={`#/changes/${encodeURIComponent(run.change_id)}`}
              className="shrink-0 text-primary hover:underline"
              title={`Change ${run.change_id}`}
            >
              change
            </a>
          ) : null}
        </div>
      </TD>
      <TD className="max-w-[220px]">
        <StatusPill kind="run" state={run.factory_result} />
        {run.error ? (
          <p className="mt-1 max-w-[200px] truncate text-[10.5px] leading-4 text-danger" title={run.error}>
            {run.error}
          </p>
        ) : null}
      </TD>
      <TD>
        <span className="block max-w-[140px] truncate text-[12px] font-medium" title={run.agent_harness || undefined}>
          {run.agent_harness || "—"}
        </span>
        {run.agent_version ? (
          <span className="mt-0.5 block max-w-[140px] truncate font-mono text-[10px] text-muted-foreground" title={run.agent_version}>
            {run.agent_version}
          </span>
        ) : null}
      </TD>
      <TD>
        <ResultSplit run={run} />
      </TD>
      <TD numeric className="whitespace-nowrap">
        {formatDuration(run.duration_ms)}
      </TD>
      <TD className="whitespace-nowrap">
        <span title={run.started_at || undefined}>{timeAgo(run.started_at)}</span>
        <span className="mt-0.5 block text-[10px] text-muted-foreground">{formatDate(run.started_at)}</span>
      </TD>
      <TD>
        <CleanupIndicator cleanup={run.cleanup_result} />
      </TD>
      <TD>
        <SecurityIndicator hardening={run.hardening} state={run.factory_result} />
      </TD>
      <TD>
        <ReviewIndicator run={run} />
      </TD>
      <TD className="w-8">
        <a
          href={href}
          aria-label={`Open run ${run.run_id}`}
          className="grid h-6 w-6 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <ChevronRight aria-hidden="true" className="h-3.5 w-3.5" />
        </a>
      </TD>
    </TR>
  );
}

export function RunsTable({ runs = [], empty = null, minWidth = 1240, className }) {
  return (
    <Table minWidth={minWidth} className={className}>
      <THead>
        <TR className="hover:bg-transparent">
          <TH>Run</TH>
          <TH>Status</TH>
          <TH>Harness</TH>
          <TH>Agent / verification</TH>
          <TH numeric>Duration</TH>
          <TH>Started</TH>
          <TH>Cleanup</TH>
          <TH>Security</TH>
          <TH>Review</TH>
          <TH className="w-8">
            <span className="sr-only">Open</span>
          </TH>
        </TR>
      </THead>
      <tbody>{runs.length > 0 ? runs.map((run) => <RunRow key={run.run_id} run={run} />) : empty}</tbody>
    </Table>
  );
}

// A denser variant used for related activations on the run detail screen.
export function RelatedRunsTable({ runs = [] }) {
  return (
    <Table minWidth={420}>
      <THead>
        <TR className="hover:bg-transparent">
          <TH>Run</TH>
          <TH>Result</TH>
          <TH numeric>Duration</TH>
          <TH>Started</TH>
        </TR>
      </THead>
      <tbody>
        {runs.map((run) => (
          <TR key={run.run_id}>
            <TD className="max-w-[220px]">
              <a
                href={runHref(run.run_id)}
                title={run.run_id}
                className="block truncate font-mono text-[11.5px] text-foreground hover:text-primary"
              >
                {run.run_id}
              </a>
              <span className="mt-0.5 block truncate text-[10px] text-muted-foreground" title={run.repository}>
                {repoLabel(run.repository)}
              </span>
            </TD>
            <TD>
              <StatusPill kind="run" state={run.factory_result} size="xs" />
            </TD>
            <TD numeric className="whitespace-nowrap">
              {formatDuration(run.duration_ms)}
            </TD>
            <TD className="whitespace-nowrap" title={run.started_at ? formatDateTime(run.started_at) : undefined}>
              {timeAgo(run.started_at)}
            </TD>
          </TR>
        ))}
      </tbody>
    </Table>
  );
}

export { RunRow };
