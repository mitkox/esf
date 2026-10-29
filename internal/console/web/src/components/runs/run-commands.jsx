import { Badge } from "@/components/ui/badge";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { CopyButton } from "@/components/ui/copy-button";
import { Terminal } from "lucide-react";
import { ReviewIndicator } from "./signals";
import { isReviewPending } from "./utils";

const COMMANDS = [
  {
    key: "approve_command",
    label: "Approve review",
    purpose: "Record a positive human review decision for this run.",
    review: true,
  },
  {
    key: "reject_command",
    label: "Reject review",
    purpose: "Record a rejection with a required note.",
    review: true,
  },
  {
    key: "cancel_command",
    label: "Cancel run",
    purpose: "Request cancellation of an in-flight workflow.",
    review: false,
  },
  {
    key: "describe_command",
    label: "Describe run",
    purpose: "Read the authoritative run description from the CLI.",
    review: false,
  },
  {
    key: "logs_command",
    label: "Stream logs",
    purpose: "Follow the workflow log stream for this run.",
    review: false,
  },
];

// OperatorCommandsCard exposes the exact CLI commands the API offers for this
// run. They are copy-only: the console is read-only in this round and never
// executes an action.
export function OperatorCommandsCard({ detail }) {
  const run = detail?.run || {};
  const actions = detail?.actions || {};
  const pending = isReviewPending(run);

  const commands = COMMANDS.filter((command) => actions[command.key]);
  if (commands.length === 0) return null;

  return (
    <Card id="operator-commands" className="scroll-mt-24">
      <CardHeader
        icon={<Terminal />}
        title="Operator commands"
        description="Copy and run these in the authenticated factory CLI. The console never executes actions."
        actions={<ReviewIndicator run={run} size="sm" />}
      />
      <CardBody padded={false}>
        <ul className="divide-y divide-border">
          {commands.map((command) => {
            const needsDecision = pending && command.review;
            return (
              <li key={command.key} className="px-4 py-2.5 sm:px-5">
                <div className="flex items-start gap-2">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="text-[11.5px] font-semibold text-foreground">{command.label}</span>
                      {needsDecision ? (
                        <Badge variant="warning" size="xs" dot>
                          Pending decision
                        </Badge>
                      ) : null}
                    </div>
                    <p className="mt-0.5 break-all font-mono text-[10.5px] leading-relaxed text-muted-foreground">
                      {actions[command.key]}
                    </p>
                    <p className="mt-0.5 text-[10px] leading-relaxed text-muted-foreground">{command.purpose}</p>
                  </div>
                  <CopyButton value={actions[command.key]} label={command.label} size="xs" />
                </div>
              </li>
            );
          })}
        </ul>
      </CardBody>
      <CardFooter className="flex-col items-start gap-1">
        <span>Read-only console: review and cancel decisions remain CLI actions in this round.</span>
        {run.review_note ? (
          <span className="break-words text-foreground">Reviewer note: {run.review_note}</span>
        ) : null}
        {run.review_error ? <span className="break-words text-warning">Review error: {run.review_error}</span> : null}
      </CardFooter>
    </Card>
  );
}
