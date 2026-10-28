import { ClipboardCheck, Info, TerminalSquare } from "lucide-react";
import { formatDateTime } from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { StatusPill } from "@/components/ui/status-pill";
import { CommandRow } from "@/components/flows/command-row";
import { Notice } from "@/components/flows/notice";

function Fact({ label, title, children }) {
  return (
    <div className="min-w-0 rounded-lg border border-border bg-surface-raised/40 px-2.5 py-1.5" title={title || undefined}>
      <p className="truncate text-[9.5px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground">{label}</p>
      <div className="mt-0.5 flex min-w-0 items-center text-[11.5px] font-medium text-foreground">{children}</div>
    </div>
  );
}

// ReviewPanel states the review gate honestly and exposes the exact CLI
// commands. The console never executes a decision.
export function ReviewPanel({ change, review, latestRun }) {
  const configured = Boolean(review?.configured);
  const globalEnabled = Boolean(review?.global_enabled);
  const pending = Boolean(review?.pending);
  const latestOutcome = review?.latest_outcome || "";
  const latestNote = latestRun?.review_note || "";

  return (
    <Card className="overflow-hidden">
      <CardHeader
        icon={<ClipboardCheck />}
        title="Human review"
        description="Decisions stay in the authenticated factory CLI; this console is a read-only evidence surface."
        actions={
          pending ? (
            <Badge variant="warning" size="sm" dot>
              Decision pending
            </Badge>
          ) : (
            <Badge variant="muted" size="sm" dot>
              No decision pending
            </Badge>
          )
        }
      />
      <CardBody className="space-y-3">
        <div className="grid grid-cols-2 gap-1.5">
          <Fact label="Gate configured" title="Whether a review gate applies to this change.">
            <Badge variant={configured ? "success" : "muted"} size="xs">
              {configured ? "Configured" : "Not configured"}
            </Badge>
          </Fact>
          <Fact label="Global review" title="factory.toml review.enabled for the whole factory.">
            <Badge variant={globalEnabled ? "info" : "muted"} size="xs">
              {globalEnabled ? "Enabled" : "Disabled"}
            </Badge>
          </Fact>
          <Fact label="Decision pending" title="Derived from the durable change status: IN_REVIEW means a decision is pending.">
            <Badge variant={pending ? "warning" : "muted"} size="xs" dot>
              {pending ? "Yes" : "No"}
            </Badge>
          </Fact>
          <Fact label="Latest outcome" title="Recorded review outcome on the latest activation run, when the manifest reports one.">
            {latestOutcome ? <StatusPill kind="review" state={latestOutcome} size="xs" /> : <span className="text-[11px] text-muted-foreground">Not reported</span>}
          </Fact>
        </div>

        {pending ? (
          <p className="text-[11px] leading-relaxed text-muted-foreground">
            Waiting since{" "}
            <span className="tabular text-foreground" title={formatDateTime(change?.updated_at)}>
              {formatDateTime(change?.updated_at)}
            </span>
            . Approve or reject from the CLI, then reload this page — the console cannot write.
          </p>
        ) : (
          <p className="text-[11px] leading-relaxed text-muted-foreground">
            The reference commands below remain the CLI entry points for a decision on this change.
          </p>
        )}

        <div className="space-y-1.5">
          <CommandRow label="Approve" command={review?.approve_command} tone="success" />
          <CommandRow
            label="Reject"
            command={review?.reject_command}
            tone="danger"
            hint={pending ? "Replace the … placeholder with a decision note." : undefined}
          />
        </div>

        {latestNote ? (
          <div className="rounded-lg border border-border bg-surface-raised/40 px-2.5 py-2">
            <p className="text-[9.5px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground">
              Latest review note
            </p>
            <p className="mt-0.5 break-words text-[11px] leading-relaxed text-foreground">{latestNote}</p>
            <p className="mt-0.5 text-[10.5px] leading-4 text-muted-foreground">
              Recorded on run <span className="mono">{latestRun.run_id}</span>
              {latestOutcome ? ` · outcome ${latestOutcome}` : ""}
            </p>
          </div>
        ) : null}

        {!configured ? (
          pending ? (
            <Notice tone="warning" icon={<Info />} title="Review gate not configured in the loaded factory.toml">
              The durable change is in review, but the current configuration does not enable a review gate for this
              scope. Check the run evidence before copying a decision command.
            </Notice>
          ) : (
            <Notice tone="muted" icon={<Info />} title="Review was not enabled for this change">
              The change did not require a human gate. The commands are shown for completeness; confirm the run state in
              the evidence before using them.
            </Notice>
          )
        ) : null}
      </CardBody>
      <CardFooter>
        <TerminalSquare aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
        <span>Commands are copied to the clipboard, never executed by the console.</span>
      </CardFooter>
    </Card>
  );
}
