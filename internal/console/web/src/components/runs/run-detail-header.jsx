import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { PageHeader } from "@/components/ui/page-header";
import { StatusPill } from "@/components/ui/status-pill";
import { formatDuration, repoLabel, shortSha, timeAgo } from "@/lib/format";
import { changeHref } from "./utils";
import { ReviewIndicator } from "./signals";

// RunDetailHeader keeps the run identity, the review state and the two
// immediate affordances (download patch, jump to CLI commands) at the top of
// the evidence viewer.
export function RunDetailHeader({ detail, live, patchAvailable, onDownloadPatch, downloadBusy, updatedAt }) {
  const run = detail?.run || {};
  const changeID = detail?.change?.change_id || run.change_id;
  const showReview = Boolean(run.review_configured || run.review_outcome || run.factory_result === "PAUSED");

  return (
    <PageHeader
      backHref="#/runs"
      backLabel="All runs"
      eyebrow="Run evidence"
      title={
        <span className="block break-all font-mono text-[16px] font-semibold leading-snug tracking-[-0.01em] sm:text-[19px]">
          {run.run_id || "Run"}
        </span>
      }
      description={
        live
          ? "Live orchestration state from the workflow engine. The durable manifest, gates and patch are written when the run completes."
          : "Immutable run manifest with its conditions, verification gates, security evidence, deliverables and artifact trail."
      }
      meta={
        <>
          {run.factory_result ? <StatusPill kind="run" state={run.factory_result} /> : null}
          {live ? (
            <Badge variant="info" dot>
              Live
            </Badge>
          ) : null}
          {showReview ? <ReviewIndicator run={run} size="sm" /> : null}
          {run.run_id ? <CopyButton value={run.run_id} label="run ID" size="xs" /> : null}
          {run.repository ? (
            <span className="min-w-0 truncate" title={run.repository}>
              {repoLabel(run.repository)}
            </span>
          ) : null}
          {run.scope ? <span>Scope {run.scope}</span> : null}
          {run.agent_harness ? <span>Harness {run.agent_harness}</span> : null}
          <span title={run.started_at || undefined}>Started {timeAgo(run.started_at)}</span>
          <span>Duration {formatDuration(run.duration_ms)}</span>
          {updatedAt ? <span>Updated {timeAgo(updatedAt)}</span> : null}
        </>
      }
      actions={
        <>
          {changeID ? (
            <Button as="a" href={changeHref(changeID)} variant="secondary" size="sm" title={`Change ${changeID}`}>
              Change {shortSha(changeID, 10)}
            </Button>
          ) : null}
          <Button
            variant="secondary"
            size="sm"
            onClick={() => document.getElementById("operator-commands")?.scrollIntoView({ behavior: "smooth", block: "start" })}
          >
            Commands
          </Button>
          {patchAvailable ? (
            <Button variant="secondary" size="sm" loading={downloadBusy} onClick={onDownloadPatch}>
              Download patch
            </Button>
          ) : null}
        </>
      }
    />
  );
}
