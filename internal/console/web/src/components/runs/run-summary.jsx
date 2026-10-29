import clsx from "clsx";
import { Boxes, Cpu, ListChecks, Recycle } from "lucide-react";
import { Card } from "@/components/ui/card";
import { StatusPill } from "@/components/ui/status-pill";
import { TONE_SOFT, TONE_TEXT } from "@/components/ui/tones";
import { formatDateTime, formatDuration, formatNumber, runStateTone } from "@/lib/format";
import { OutcomeBadge } from "./signals";
import { cleanupMeta, outcomeMeta } from "./utils";

function SummaryCard({ label, icon, tone = "muted", value, details = [] }) {
  return (
    <Card className="overflow-hidden">
      <div className="px-3.5 pb-3 pt-3">
        <div className="flex items-center gap-2">
          <span
            aria-hidden="true"
            className={clsx(
              "grid h-6 w-6 shrink-0 place-items-center rounded-md [&>svg]:h-3.5 [&>svg]:w-3.5",
              TONE_SOFT[tone] || TONE_SOFT.muted,
              TONE_TEXT[tone] || TONE_TEXT.muted,
            )}
          >
            {icon}
          </span>
          <p className="truncate text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">{label}</p>
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-1.5">{value}</div>
        {details.length > 0 ? (
          <div className="mt-1.5 space-y-0.5 text-[11px] leading-4 text-muted-foreground">
            {details.map((detail, index) => (
              <p key={index} className="break-words" title={typeof detail === "string" ? detail : undefined}>
                {detail}
              </p>
            ))}
          </div>
        ) : null}
      </div>
    </Card>
  );
}

// RunSummaryCards answers "what happened" for the four independently recorded
// outcomes of a run. Each value is exactly the enum the manifest recorded.
export function RunSummaryCards({ run, verification }) {
  if (!run) return null;
  const steps = Array.isArray(verification?.steps) ? verification.steps : [];
  const passedSteps = steps.filter((step) => step.passed).length;
  const agentMeta = outcomeMeta(run.agent_result);
  const verifyMeta = outcomeMeta(run.verification_result);
  const cleanup = cleanupMeta(run.cleanup_result);

  const factoryDetails = [`Duration ${formatDuration(run.duration_ms)}`];
  if (run.started_at) factoryDetails.push(`Started ${formatDateTime(run.started_at)}`);
  if (run.error) factoryDetails.push(run.error);

  const agentDetails = [];
  if (run.agent_harness) agentDetails.push(`Harness ${run.agent_harness}${run.agent_version ? ` · ${run.agent_version}` : ""}`);
  if (run.tokens_in !== null && run.tokens_in !== undefined) {
    agentDetails.push(`Tokens ${formatNumber(run.tokens_in)} in / ${formatNumber(run.tokens_out)} out`);
  }

  const verifyDetails = [];
  if (verification?.profile) verifyDetails.push(`Profile ${verification.profile}`);
  if (verification?.available) {
    verifyDetails.push(`${formatNumber(passedSteps)} of ${formatNumber(steps.length)} gates passed`);
  } else {
    verifyDetails.push("No verification result recorded");
  }

  const cleanupDetails = [cleanup.detail];
  if (run.cleanup_result?.remaining_sandboxes !== undefined && run.cleanup_result?.outcome) {
    cleanupDetails.push(`Sandboxes remaining: ${formatNumber(run.cleanup_result.remaining_sandboxes)}`);
  }

  return (
    <div className="mb-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <SummaryCard
        label="Factory outcome"
        icon={<Boxes />}
        tone={runStateTone(run.factory_result)}
        value={<StatusPill kind="run" state={run.factory_result} />}
        details={factoryDetails}
      />
      <SummaryCard
        label="Agent"
        icon={<Cpu />}
        tone={agentMeta.tone}
        value={<OutcomeBadge value={run.agent_result} />}
        details={agentDetails}
      />
      <SummaryCard
        label="Verification"
        icon={<ListChecks />}
        tone={verifyMeta.tone}
        value={<OutcomeBadge value={run.verification_result} />}
        details={verifyDetails}
      />
      <SummaryCard
        label="Sandbox cleanup"
        icon={<Recycle />}
        tone={cleanup.tone}
        value={<StatusPill kind="cleanup" state={run.cleanup_result} />}
        details={cleanupDetails}
      />
    </div>
  );
}
