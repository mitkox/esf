import { ScrollText } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { Table, THead, TH, TR, TD, TableEmpty } from "@/components/ui/table";
import { formatDateTime, timeAgo, titleCase } from "@/lib/format";
import { describeAuditDetail } from "./utils";

const ACTION_VARIANTS = {
  attach: "info",
  preview: "info",
  suspend: "muted",
  resume: "muted",
  "review-decision": "security",
};

const OUTCOME_VARIANTS = {
  SUCCESS: "success",
  FAILED: "danger",
  SKIPPED: "muted",
  ATTEMPTED: "info",
};

// AuditCard lists the privileged operator actions recorded alongside the run.
// Command output is deliberately not stored in the audit trail; only bounded
// action context is shown.
export function AuditCard({ entries = [] }) {
  return (
    <Card>
      <CardHeader
        icon={<ScrollText />}
        title="Audit trail"
        description="Privileged operator actions recorded for this run. Command output is not stored in the audit trail."
        actions={
          <Badge variant="muted" size="xs">
            {entries.length} action{entries.length === 1 ? "" : "s"}
          </Badge>
        }
      />
      <CardBody padded={false}>
        <Table minWidth={760} className="max-h-[420px] overflow-y-auto">
          <THead>
            <TR className="hover:bg-transparent">
              <TH>When</TH>
              <TH>Action</TH>
              <TH>Actor</TH>
              <TH>Outcome</TH>
              <TH>Detail</TH>
            </TR>
          </THead>
          <tbody>
            {entries.length === 0 ? (
              <TableEmpty
                colSpan={5}
                icon={<ScrollText />}
                title="No audit actions recorded"
                description="No attach, preview, suspend, resume or review decisions were recorded for this run."
              />
            ) : (
              entries.map((entry, index) => {
                const detail = describeAuditDetail(entry.detail);
                return (
                  <TR key={`${entry.at}-${entry.action}-${index}`}>
                    <TD className="whitespace-nowrap text-[11.5px]" title={entry.at ? formatDateTime(entry.at) : undefined}>
                      {timeAgo(entry.at)}
                    </TD>
                    <TD>
                      <Badge variant={ACTION_VARIANTS[entry.action] || "outline"} size="xs">
                        {entry.action || "unknown"}
                      </Badge>
                    </TD>
                    <TD className="whitespace-nowrap text-[11.5px]">{entry.actor || "—"}</TD>
                    <TD>
                      <Badge variant={OUTCOME_VARIANTS[String(entry.outcome || "").toUpperCase()] || "outline"} size="xs" dot>
                        {titleCase(entry.outcome || "unknown")}
                      </Badge>
                    </TD>
                    <TD className="max-w-[360px]">
                      {detail ? (
                        <span className="block truncate font-mono text-[10.5px] text-muted-foreground" title={detail}>
                          {detail}
                        </span>
                      ) : (
                        <span className="text-[11px] text-muted-foreground">—</span>
                      )}
                      {entry.error ? (
                        <span className="mt-0.5 block truncate text-[10.5px] text-danger" title={entry.error}>
                          {entry.error}
                        </span>
                      ) : null}
                    </TD>
                  </TR>
                );
              })
            )}
          </tbody>
        </Table>
      </CardBody>
    </Card>
  );
}
