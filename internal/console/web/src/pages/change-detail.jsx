import { useCallback, useMemo } from "react";
import { ClipboardCheck, FileWarning, GitBranch, Play, TriangleAlert } from "lucide-react";
import { useApi } from "@/lib/hooks";
import { useWorkspace } from "@/lib/workspace";
import {
  formatDate,
  formatDateTime,
  formatNumber,
  repoLabel,
  shortSha,
  timeAgo,
} from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { StatusPill } from "@/components/ui/status-pill";
import { CopyButton } from "@/components/ui/copy-button";
import { StatStrip, StatStripSkeleton } from "@/components/flows/stat-strip";
import { ErrorPanel } from "@/components/flows/error-panel";
import { Notice } from "@/components/flows/notice";
import { RefreshControl } from "@/components/flows/refresh-control";
import { ActivationTable } from "@/components/flows/change-detail/activation-table";
import { ChangeMetaPanel } from "@/components/flows/change-detail/meta-panel";
import { ReviewPanel } from "@/components/flows/change-detail/review-panel";
import { TaskPanel } from "@/components/flows/change-detail/task-panel";
import { changeSpend, pluralize } from "@/components/flows/helpers";

const CONTAINER = "mx-auto w-full max-w-[1600px] px-4 py-5 sm:px-6 lg:px-8";
const STRIP_COLUMNS = "grid-cols-2 sm:grid-cols-3 xl:grid-cols-7";

function DetailSkeleton() {
  return (
    <div className={CONTAINER}>
      <div className="mb-5 space-y-3">
        <div className="skeleton h-3 w-20" />
        <div className="skeleton h-7 w-80 max-w-full" />
        <div className="skeleton h-4 w-96 max-w-full" />
        <div className="flex gap-2 pt-1">
          <div className="skeleton h-5 w-20 rounded-full" />
          <div className="skeleton h-5 w-28 rounded-full" />
          <div className="skeleton h-5 w-24 rounded-full" />
        </div>
      </div>
      <div className="space-y-4">
        <StatStripSkeleton items={7} columnsClassName={STRIP_COLUMNS} />
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-12">
          <div className="skeleton h-64 lg:col-span-8" />
          <div className="skeleton h-64 lg:col-span-4" />
        </div>
      </div>
    </div>
  );
}

function ChangeNotFound({ changeID }) {
  return (
    <div className={CONTAINER}>
      <PageHeader
        eyebrow="Change"
        title={<span className="mono break-all">{changeID || "Unknown change"}</span>}
        backHref="#/changes"
        backLabel="All changes"
      />
      <Card>
        <EmptyState
          icon={<FileWarning aria-hidden="true" />}
          title="Change not found"
          description={
            changeID
              ? `No durable change record matches “${changeID}”. It may have been pruned, or the link may be incorrect.`
              : "This route has no change identifier."
          }
          action={
            <div className="flex flex-wrap items-center justify-center gap-2">
              <Button as="a" href="#/changes" variant="secondary" size="sm" icon={<GitBranch />}>
                Back to changes
              </Button>
              <Button as="a" href="#/reviews" variant="ghost" size="sm" icon={<ClipboardCheck />}>
                Review queue
              </Button>
            </div>
          }
        />
      </Card>
    </div>
  );
}

export function ChangeDetailPage({ changeID }) {
  const { autoRefresh, refreshNonce, refreshAll } = useWorkspace();
  const path = changeID ? `/changes/${encodeURIComponent(changeID)}` : "";
  const { data, error, loading, updatedAt } = useApi(path, {
    enabled: Boolean(changeID),
    pollMs: autoRefresh ? 30000 : 0,
    deps: [refreshNonce],
  });
  const handleRefresh = useCallback(() => refreshAll(), [refreshAll]);

  const change = data?.change || null;
  const runs = useMemo(() => (Array.isArray(data?.runs) ? data.runs : []), [data]);
  const review = data?.review || {};
  const warnings = useMemo(() => (Array.isArray(data?.warnings) ? data.warnings : []), [data]);
  const latest = runs.length ? runs[0] : null;

  const warningNotes = useMemo(() => {
    if (!change) return [];
    const evidencePattern = /no run evidence|evidence is unavailable|pruned/i;
    const notes = warnings.map((warning) => ({
      key: warning,
      title: evidencePattern.test(warning) ? "No retained run evidence" : "Recorded warning",
      body: warning,
    }));
    const hasEvidenceWarning = warnings.some((warning) => evidencePattern.test(warning));
    if ((!change.has_evidence || runs.length === 0) && !hasEvidenceWarning) {
      notes.push({
        key: "evidence",
        title: "No retained run evidence",
        body: `The durable change record reports has_evidence=${String(change.has_evidence)} and ${runs.length} resolved activation run${
          runs.length === 1 ? "" : "s"
        }. The change is still auditable, but gates, patch and run-level evidence cannot be shown here.`,
      });
    }
    return notes;
  }, [change, warnings, runs.length]);

  const stats = useMemo(() => {
    if (!change) return [];
    const spend = changeSpend(change);
    const activationCount = Array.isArray(change.activations) ? change.activations.length : 0;
    return [
      {
        key: "status",
        label: "Status",
        value: <StatusPill kind="change" state={change.status} />,
        sub: "durable lifecycle",
        hint: "Durable change status from the change artifact. Review decisions never mutate it from the console.",
      },
      {
        key: "attempts",
        label: "Attempts",
        value: formatNumber(change.attempts),
        sub: "recorded activations",
        tone: Number(change.attempts) > 1 ? "warning" : "",
        hint: "Number of recorded activation attempts from the durable change record.",
      },
      {
        key: "tokens",
        label: "Total tokens",
        value: spend.tokensLabel,
        muted: spend.tokens === 0,
        sub: "aggregate",
        hint: "Sum of total_tokens recorded on the change. Zero means not reported, never estimated.",
      },
      {
        key: "cost",
        label: "Total cost",
        value: spend.costLabel,
        muted: !spend.reported,
        sub: "reported cost",
        hint: "Sum of total_cost_usd recorded on the change. The console never estimates cost.",
      },
      {
        key: "runs",
        label: "Resolved runs",
        value: formatNumber(runs.length),
        sub: `${pluralize(activationCount, "activation")} recorded`,
        hint: "Activation runs resolved from the artifact store for this change.",
      },
      {
        key: "created",
        label: "Created",
        value: timeAgo(change.created_at),
        sub: formatDate(change.created_at),
        hint: formatDateTime(change.created_at),
      },
      {
        key: "updated",
        label: "Updated",
        value: timeAgo(change.updated_at),
        sub: formatDate(change.updated_at),
        hint: formatDateTime(change.updated_at),
      },
    ];
  }, [change, runs.length]);

  if (!changeID) return <ChangeNotFound changeID="" />;
  if (error && error?.status === 404) return <ChangeNotFound changeID={changeID} />;
  if (error && !data) {
    return (
      <div className={CONTAINER}>
        <PageHeader eyebrow="Change" title={<span className="mono break-all">{changeID}</span>} backHref="#/changes" backLabel="All changes" />
        <ErrorPanel error={error} onRetry={handleRefresh} title="The change record could not be loaded" />
      </div>
    );
  }
  if (loading && !data) return <DetailSkeleton />;
  if (!change) return <ChangeNotFound changeID={changeID} />;

  return (
    <div className={CONTAINER}>
      <PageHeader
        eyebrow="Change"
        backHref="#/changes"
        backLabel="All changes"
        title={<span className="mono break-all">{change.change_id || changeID}</span>}
        description="Lifecycle, activation lineage and the human review gate for one durable change."
        actions={
          <>
            <RefreshControl updatedAt={data?.generated_at || updatedAt} loading={loading} onRefresh={handleRefresh} />
            {latest ? (
              <Button
                as="a"
                href={`#/runs/${encodeURIComponent(latest.run_id)}`}
                variant="secondary"
                size="sm"
                icon={<Play />}
              >
                Latest run
              </Button>
            ) : null}
            <CopyButton value={change.change_id || changeID} label="change ID" size="md" variant="outline" className="shrink-0" />
          </>
        }
        meta={
          <>
            <StatusPill kind="change" state={change.status} />
            <span className="inline-flex min-w-0 items-center gap-1.5">
              <GitBranch aria-hidden="true" className="h-3 w-3 shrink-0" />
              <span className="min-w-0 truncate" title={change.repository}>
                {repoLabel(change.repository)}
              </span>
            </span>
            <Badge variant="muted" size="sm">
              {change.scope || "no scope"}
            </Badge>
            {change.requested_revision ? (
              <span className="mono" title={change.requested_revision}>
                rev {shortSha(change.requested_revision)}
              </span>
            ) : null}
            {change.last_result ? <StatusPill kind="run" state={change.last_result} /> : null}
            <span title={formatDateTime(change.updated_at)}>Updated {timeAgo(change.updated_at)}</span>
            {autoRefresh ? <span>Auto-refresh 30s</span> : null}
          </>
        }
      />

      <div className="space-y-4">
        {warningNotes.length ? (
          <div className="space-y-2" role="status">
            {warningNotes.map((note) => (
              <Notice key={note.key} tone="warning" title={note.title} icon={<TriangleAlert />}>
                {note.body}
              </Notice>
            ))}
          </div>
        ) : null}

        {change.rework_note ? (
          <Notice tone="info" title="Rework requested" icon={<GitBranch />}>
            {change.rework_note}
          </Notice>
        ) : null}

        <StatStrip items={stats} label="Change lifecycle and aggregate spend" columnsClassName={STRIP_COLUMNS} />

        {error ? (
          <Notice tone="warning" title="Refresh failed" icon={<FileWarning />}>
            {error.message} Showing evidence generated {data?.generated_at ? timeAgo(data.generated_at) : "recently"}.
          </Notice>
        ) : null}

        <div className="grid grid-cols-1 gap-4 lg:grid-cols-12">
          <div className="min-w-0 space-y-4 lg:col-span-8">
            <ActivationTable change={change} runs={runs} />
            <TaskPanel change={change} />
          </div>
          <div className="min-w-0 space-y-4 lg:col-span-4">
            <ReviewPanel change={change} review={review} latestRun={latest} />
            <ChangeMetaPanel change={change} runs={runs} />
          </div>
        </div>

        <p className="px-1 text-[10.5px] leading-relaxed text-muted-foreground">
          Evidence source: <span className="mono">changes/{change.change_id || changeID}.json</span>, the activation run
          manifests, and the loaded <span className="mono">factory.toml</span> review scope. Decisions remain in the
          factory CLI.
        </p>
      </div>
    </div>
  );
}
