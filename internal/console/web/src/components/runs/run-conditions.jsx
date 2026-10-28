import clsx from "clsx";
import { Radio, Workflow } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { TONE_DOT } from "@/components/ui/tones";
import { formatDateTime, timeAgo, titleCase } from "@/lib/format";

const STATUS = {
  True: { label: "True", tone: "success" },
  False: { label: "False", tone: "danger" },
  Unknown: { label: "Unknown", tone: "muted" },
};

function statusMeta(status) {
  return STATUS[status] || { label: titleCase(status || "Unknown"), tone: "muted" };
}

// Condition types are camelCase machine identifiers (EgressVerified,
// SandboxReady). Insert word breaks so the timeline reads as prose while the
// machine identifiers stay recognisable.
function conditionLabel(type) {
  if (!type) return "Condition";
  return titleCase(String(type).replace(/([a-z0-9])([A-Z])/g, "$1 $2").replace(/[_-]+/g, " "));
}

// ConditionsCard renders the per-step conditions as a vertical timeline. A
// condition is never removed from the manifest, so "cleanup verified" and
// "metadata probe undecided" stay visible as recorded facts.
export function ConditionsCard({ conditions = [], live = false }) {
  return (
    <Card>
      <CardHeader
        icon={<Workflow />}
        title="Conditions"
        description={
          live
            ? "Workflow step outcomes from the live Temporal query; durable conditions are written when the run completes."
            : "Per-step outcomes recorded in the durable run manifest, in lifecycle order."
        }
        actions={
          <>
            {live ? (
              <Badge variant="info" dot>
                Live
              </Badge>
            ) : null}
            <Badge variant="muted" size="xs">
              {conditions.length} condition{conditions.length === 1 ? "" : "s"}
            </Badge>
          </>
        }
      />
      <CardBody padded={false}>
        {conditions.length === 0 ? (
          <EmptyState
            compact
            icon={<Workflow />}
            title="No conditions recorded"
            description={
              live
                ? "The live workflow has not reported step conditions yet."
                : "The manifest does not contain any step conditions for this run."
            }
          />
        ) : (
          <ol className="divide-y divide-border">
            {conditions.map((condition, index) => {
              const meta = statusMeta(condition.status);
              const last = index === conditions.length - 1;
              return (
                <li key={`${condition.type || "condition"}-${index}`} className="flex gap-3 px-4 py-2.5 sm:px-5">
                  <div className="flex flex-col items-center pt-1.5">
                    <span aria-hidden="true" className={clsx("h-2 w-2 shrink-0 rounded-full", TONE_DOT[meta.tone] || TONE_DOT.muted)} />
                    {!last ? <span aria-hidden="true" className="mt-1 w-px flex-1 bg-border" /> : null}
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                      <span className="text-[12px] font-semibold leading-5 text-foreground">
                        {conditionLabel(condition.type)}
                      </span>
                      <Badge variant={meta.tone === "muted" ? "muted" : meta.tone} size="xs" dot>
                        {meta.label}
                      </Badge>
                      {condition.reason ? (
                        <span className="font-mono text-[10.5px] text-muted-foreground">{condition.reason}</span>
                      ) : null}
                      <span
                        className="ml-auto shrink-0 text-[10px] text-muted-foreground"
                        title={condition.last_transition_time ? formatDateTime(condition.last_transition_time) : undefined}
                      >
                        {condition.last_transition_time ? timeAgo(condition.last_transition_time) : "—"}
                      </span>
                    </div>
                    {condition.message ? (
                      <p className="mt-1 break-words font-mono text-[10.5px] leading-relaxed text-muted-foreground">
                        {condition.message}
                      </p>
                    ) : null}
                  </div>
                </li>
              );
            })}
          </ol>
        )}
      </CardBody>
    </Card>
  );
}
