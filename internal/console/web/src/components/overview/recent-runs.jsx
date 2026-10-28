import clsx from "clsx";
import { History } from "lucide-react";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { StatusPill } from "@/components/ui/status-pill";
import { Table, THead, TH, TR, TD, TableEmpty } from "@/components/ui/table";
import { formatDateTime, formatDuration, repoLabel, timeAgo } from "@/lib/format";

// RecentRunsCard is the compact audit entry point: the newest activations with
// just enough context to decide whether a run needs a closer look.
export function RecentRunsCard({ runs = [], className, now }) {
  const list = Array.isArray(runs) ? runs : [];

  return (
    <Card className={clsx("min-w-0", className)}>
      <CardHeader
        icon={<History />}
        title="Recent runs"
        description="Newest activations in the evidence store"
        actions={
          <Button href="#/runs" variant="ghost" size="xs">
            All runs
          </Button>
        }
      />
      <CardBody padded={false}>
        <Table minWidth={780} aria-label="Recent runs">
          <THead>
            <tr>
              <TH>Run</TH>
              <TH>Status</TH>
              <TH>Harness</TH>
              <TH>Repository</TH>
              <TH numeric>Duration</TH>
              <TH>Started</TH>
            </tr>
          </THead>
          <tbody>
            {list.length ? (
              list.map((run) => (
                <TR key={run.run_id}>
                  <TD>
                    <a
                      href={`#/runs/${encodeURIComponent(run.run_id)}`}
                      className="mono block max-w-[260px] truncate font-medium text-foreground transition-colors hover:text-primary"
                      title={run.run_id}
                    >
                      {run.run_id}
                    </a>
                  </TD>
                  <TD>
                    <StatusPill kind="run" state={run.factory_result} size="xs" />
                  </TD>
                  <TD muted>
                    <span className="block max-w-[150px] truncate" title={run.agent_harness}>
                      {run.agent_harness || "—"}
                    </span>
                  </TD>
                  <TD muted>
                    <span className="block max-w-[260px] truncate" title={run.repository}>
                      {repoLabel(run.repository)}
                    </span>
                  </TD>
                  <TD numeric muted>
                    {run.duration_ms === null || run.duration_ms === undefined ? "—" : formatDuration(run.duration_ms)}
                  </TD>
                  <TD muted>
                    <span title={formatDateTime(run.started_at)}>{timeAgo(run.started_at, now)}</span>
                  </TD>
                </TR>
              ))
            ) : (
              <TableEmpty
                colSpan={6}
                title="No runs recorded"
                description="Activations appear here as soon as the factory writes a run manifest."
              />
            )}
          </tbody>
        </Table>
      </CardBody>
      <CardFooter>
        <span className="truncate">Newest activations are shown regardless of the selected window.</span>
        <Button href="#/runs" variant="ghost" size="xs" className="ml-auto">
          Open runs ledger
        </Button>
      </CardFooter>
    </Card>
  );
}
