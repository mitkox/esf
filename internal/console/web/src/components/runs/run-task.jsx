import { useState } from "react";
import clsx from "clsx";
import { ChevronDown, ChevronUp, FileWarning, NotebookText } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { CopyButton } from "@/components/ui/copy-button";
import { EmptyState } from "@/components/ui/empty-state";
import { formatDateTime, timeAgo } from "@/lib/format";

// TaskCard shows the human-facing task specification recorded with the run.
// Long specifications collapse to a fixed height; the full text is still
// available through the toggle and the copy button.
export function TaskCard({ task, live }) {
  const [expanded, setExpanded] = useState(false);
  const text = task?.task || "";
  const collapsible = text.length > 900;
  const truncated = Boolean(task?.truncated);

  return (
    <Card>
      <CardHeader
        icon={<NotebookText />}
        title="Task specification"
        description="The task text recorded in task.json, redacted and bounded by the factory."
        actions={
          <>
            {truncated ? (
              <Badge variant="warning" size="xs">
                Truncated evidence
              </Badge>
            ) : null}
            {task?.task_hash ? <Badge variant="muted" size="xs">{task.task_hash.slice(0, 12)}</Badge> : null}
            {text ? <CopyButton value={text} label="task specification" size="sm" /> : null}
          </>
        }
      />
      <CardBody>
        {!task ? (
          <EmptyState
            compact
            icon={<FileWarning />}
            title={live ? "Task evidence not written yet" : "Task specification unavailable"}
            description={
              live
                ? "The task specification is recorded when the durable run evidence is written."
                : "task.json is missing or unreadable for this run. Reference the evidence browser for whatever was collected."
            }
          />
        ) : (
          <>
            <div className="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
              {task.agent_harness ? (
                <span>
                  Harness <span className="font-mono text-foreground">{task.agent_harness}</span>
                </span>
              ) : null}
              {task.agent_model || task.model ? (
                <span>
                  Model <span className="font-mono text-foreground">{task.agent_model || task.model}</span>
                </span>
              ) : null}
              {task.egress_policy ? (
                <span>
                  Egress <span className="font-mono text-foreground">{task.egress_policy}</span>
                </span>
              ) : null}
              {task.verification_profile ? (
                <span>
                  Gates <span className="font-mono text-foreground">{task.verification_profile}</span>
                </span>
              ) : null}
              {task.recorded_at ? (
                <span title={formatDateTime(task.recorded_at)}>Recorded {timeAgo(task.recorded_at)}</span>
              ) : null}
            </div>
            <pre
              className={clsx(
                "scroll-thin whitespace-pre-wrap break-words rounded-lg border border-border bg-surface-raised px-3 py-2.5 font-mono text-[11.5px] leading-relaxed text-foreground",
                !expanded && collapsible && "max-h-[16rem] overflow-hidden",
              )}
            >
              {text || "The task specification is empty."}
            </pre>
            {collapsible ? (
              <div className="mt-2 flex items-center gap-2">
                <Button
                  variant="ghost"
                  size="xs"
                  icon={expanded ? <ChevronUp /> : <ChevronDown />}
                  onClick={() => setExpanded((value) => !value)}
                  aria-expanded={expanded}
                >
                  {expanded ? "Collapse task" : "Show full task"}
                </Button>
                <span className="text-[10.5px] text-muted-foreground">
                  {text.length.toLocaleString("en-US")} characters
                </span>
              </div>
            ) : null}
            {truncated ? (
              <p className="mt-1.5 text-[10.5px] leading-relaxed text-warning">
                The recorded task was truncated by the console evidence limit; the original specification may be longer.
              </p>
            ) : null}
          </>
        )}
      </CardBody>
    </Card>
  );
}
