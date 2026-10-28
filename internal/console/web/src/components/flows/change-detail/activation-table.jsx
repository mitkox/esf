import { FileWarning, GitBranch } from "lucide-react";
import { formatDateTime, formatDuration, formatNumber, shortSha, timeAgo } from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { Card, CardFooter, CardHeader } from "@/components/ui/card";
import { StatusPill } from "@/components/ui/status-pill";
import { Table, THead, TH, TR, TD } from "@/components/ui/table";
import { TableEmpty } from "@/components/ui/table";
import { activationReason, missingActivationRuns, runTokenTotal } from "@/components/flows/helpers";

function ReviewCell({ run }) {
  if (!run?.review_configured) {
    return (
      <span className="text-[11px] text-muted-foreground" title="Review was not configured for this run.">
        Not required
      </span>
    );
  }
  if (!run.review_outcome) {
    return <span className="text-[11px] text-muted-foreground">No outcome reported</span>;
  }
  return <StatusPill kind="review" state={run.review_outcome} />;
}

// ActivationTable is the lineage view: every run resolved for the change, with
// the activation reason when the lineage recorded one.
export function ActivationTable({ change, runs }) {
  const list = Array.isArray(runs) ? runs : [];
  const missing = missingActivationRuns(change, list);
  const activations = Array.isArray(change?.activations) ? change.activations : [];
  const resolved = activations.filter((activation) => list.some((run) => run.run_id === activation.run_id)).length;

  return (
    <Card className="overflow-hidden">
      <CardHeader
        icon={<GitBranch />}
        title="Activation runs"
        description="Runs recorded for this change, newest first. Reasons come from the change activation lineage."
        actions={
          <Badge variant="muted" size="sm">
            {formatNumber(list.length)} {list.length === 1 ? "run" : "runs"}
          </Badge>
        }
      />
      <Table minWidth={860}>
        <THead>
          <TR>
            <TH>Run</TH>
            <TH>Reason</TH>
            <TH>Result</TH>
            <TH>Review</TH>
            <TH>Harness</TH>
            <TH numeric>Duration</TH>
            <TH>Started</TH>
          </TR>
        </THead>
        <tbody>
          {list.length === 0 ? (
            <TableEmpty
              colSpan={7}
              icon={<FileWarning aria-hidden="true" />}
              title="No activation runs available"
              description="The change record references activations, but their run evidence is no longer retained in the artifact store."
            />
          ) : (
            list.map((run) => {
              const reason = activationReason(change, run.run_id);
              const tokens = runTokenTotal(run);
              return (
                <TR key={run.run_id} className="relative">
                  <TD>
                    <div className="flex min-w-0 flex-col gap-0.5">
                      <a
                        href={`#/runs/${encodeURIComponent(run.run_id)}`}
                        title={run.run_id}
                        className="mono min-w-0 truncate text-[12px] font-medium text-foreground underline-offset-4 after:absolute after:inset-0 after:z-0 after:content-[''] hover:text-primary hover:underline focus-visible:outline-offset-4"
                      >
                        {run.run_id}
                      </a>
                      <span className="text-[10.5px] leading-4 text-muted-foreground">
                        {run.baseline_sha ? `baseline ${shortSha(run.baseline_sha)}` : "baseline not reported"}
                        {tokens !== null ? ` · ${formatNumber(tokens)} tok` : ""}
                      </span>
                    </div>
                  </TD>
                  <TD>
                    {reason ? (
                      <Badge variant="outline" size="sm" title={`Activation reason: ${reason}`}>
                        {reason}
                      </Badge>
                    ) : (
                      <span className="text-[11px] text-muted-foreground" title="This run is not referenced by the change activation lineage.">
                        Not in lineage
                      </span>
                    )}
                  </TD>
                  <TD>
                    <StatusPill kind="run" state={run.factory_result} />
                  </TD>
                  <TD>
                    <ReviewCell run={run} />
                  </TD>
                  <TD muted>
                    <span className="text-[11.5px]" title={run.agent_version || undefined}>
                      {run.agent_harness || "—"}
                    </span>
                  </TD>
                  <TD numeric>{formatDuration(run.duration_ms)}</TD>
                  <TD muted>
                    <span title={formatDateTime(run.started_at)}>{timeAgo(run.started_at)}</span>
                  </TD>
                </TR>
              );
            })
          )}
        </tbody>
      </Table>
      {missing.length ? (
        <CardFooter className="text-warning">
          <FileWarning aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
          <span>
            {formatNumber(missing.length)} activation{missing.length === 1 ? " has" : "s have"} no retained run evidence:
          </span>
          <span className="mono min-w-0 truncate text-[10.5px] text-muted-foreground" title={missing.map((activation) => activation.run_id).join(", ")}>
            {missing.map((activation) => activation.run_id).join(", ")}
          </span>
        </CardFooter>
      ) : activations.length ? (
        <CardFooter>
          <span className="tabular">
            {formatNumber(resolved)} of {formatNumber(activations.length)} activation
            {activations.length === 1 ? "" : "s"} resolved to retained run evidence.
          </span>
        </CardFooter>
      ) : null}
    </Card>
  );
}
