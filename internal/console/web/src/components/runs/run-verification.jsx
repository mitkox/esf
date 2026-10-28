import clsx from "clsx";
import { CircleCheck, CircleX, FileText, ListChecks, TimerOff } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { TONE_SOFT, TONE_TEXT } from "@/components/ui/tones";
import { formatDuration, timeAgo } from "@/lib/format";

function gateStatus(step) {
  if (step.timed_out) return { tone: "warning", label: "Timed out" };
  if (step.passed) return { tone: "success", label: "Passed" };
  return { tone: "danger", label: "Failed" };
}

function GateRow({ step, onPreviewArtifact }) {
  const status = gateStatus(step);
  const Icon = step.timed_out ? TimerOff : step.passed ? CircleCheck : CircleX;
  const exitTone = step.exit_code === 0 ? "text-success" : "text-danger";

  return (
    <li className="px-4 py-3 sm:px-5">
      <div className="flex flex-wrap items-start gap-x-3 gap-y-2">
        <span
          aria-hidden="true"
          className={clsx(
            "mt-0.5 grid h-6 w-6 shrink-0 place-items-center rounded-full [&>svg]:h-3.5 [&>svg]:w-3.5",
            TONE_SOFT[status.tone] || TONE_SOFT.muted,
            TONE_TEXT[status.tone] || TONE_TEXT.muted,
          )}
        >
          <Icon />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="font-mono text-[12px] font-semibold leading-5 text-foreground">{step.id}</span>
            {step.description ? <span className="text-[11px] leading-5 text-muted-foreground">{step.description}</span> : null}
            <Badge variant={step.mandatory ? "outline" : "muted"} size="xs">
              {step.mandatory ? "Mandatory" : "Optional"}
            </Badge>
            {step.timed_out ? (
              <Badge variant="warning" size="xs">
                Timed out
              </Badge>
            ) : null}
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
            <span className="inline-flex min-w-0 max-w-full items-center gap-1 font-mono text-foreground" title={(step.argv || []).join(" ")}>
              <span className="shrink-0 text-muted-foreground">$</span>
              <span className="truncate">{(step.argv || []).join(" ")}</span>
            </span>
            <span>
              exit <span className={clsx("tabular font-medium", exitTone)}>{step.exit_code}</span>
            </span>
            <span className="tabular">{formatDuration(step.duration_ms)}</span>
            {step.completed_at ? <span title={step.completed_at}>finished {timeAgo(step.completed_at)}</span> : null}
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-1.5">
          {step.stdout_artifact && onPreviewArtifact ? (
            <Button
              variant="ghost"
              size="xs"
              icon={<FileText />}
              title={`Preview ${step.stdout_artifact}`}
              aria-label={`Preview ${step.stdout_artifact}`}
              onClick={() => onPreviewArtifact(step.stdout_artifact, `${step.id} stdout`)}
            >
              stdout
            </Button>
          ) : null}
          {step.stderr_artifact && onPreviewArtifact ? (
            <Button
              variant="ghost"
              size="xs"
              icon={<FileText />}
              title={`Preview ${step.stderr_artifact}`}
              aria-label={`Preview ${step.stderr_artifact}`}
              onClick={() => onPreviewArtifact(step.stderr_artifact, `${step.id} stderr`)}
            >
              stderr
            </Button>
          ) : null}
        </div>
      </div>
    </li>
  );
}

// VerificationCard shows the deterministic gate program result. Gate output
// artifacts are previewed through the evidence API, never rendered from local
// guesses about the repository.
export function VerificationCard({ verification, onPreviewArtifact, live = false }) {
  const available = Boolean(verification?.available);
  const steps = Array.isArray(verification?.steps) ? verification.steps : [];
  const passed = steps.filter((step) => step.passed).length;

  return (
    <Card>
      <CardHeader
        icon={<ListChecks />}
        title="Verification gates"
        description="Deterministic gate programs executed against the resulting tree."
        actions={
          available ? (
            <>
              <Badge variant={verification.passed ? "success" : "danger"} size="xs" dot>
                {verification.passed ? "All gates passed" : "Gates failed"}
              </Badge>
              <Badge variant="muted" size="xs">
                {passed}/{steps.length} passed
              </Badge>
            </>
          ) : (
            <Badge variant="warning" size="xs">
              Unavailable
            </Badge>
          )
        }
      />
      <CardBody padded={false}>
        {!available ? (
          <EmptyState
            compact
            icon={<ListChecks />}
            title={live ? "Verification not available yet" : "No verification evidence"}
            description={
              live
                ? "Gates run after the agent finishes; no verification result is written for this live run yet."
                : "verification/result.json is missing or unreadable for this run. Gate status cannot be judged from this evidence."
            }
          />
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-border px-4 py-2.5 text-[11px] text-muted-foreground sm:px-5">
              <span>
                Profile <span className="font-mono text-foreground">{verification.profile || "—"}</span>
              </span>
              <span>
                Duration <span className="tabular text-foreground">{formatDuration(verification.duration_ms)}</span>
              </span>
              {verification.started_at ? (
                <span title={verification.started_at}>Started {timeAgo(verification.started_at)}</span>
              ) : null}
              <span className="tabular">
                {passed} of {steps.length} gates passed
              </span>
            </div>
            {steps.length > 0 ? (
              <ol className="divide-y divide-border">
                {steps.map((step) => (
                  <GateRow key={step.id} step={step} onPreviewArtifact={onPreviewArtifact} />
                ))}
              </ol>
            ) : (
              <EmptyState
                compact
                icon={<ListChecks />}
                title="No gate steps recorded"
                description="The verification result exists but contains no gate steps."
              />
            )}
          </>
        )}
      </CardBody>
    </Card>
  );
}
