import { ExternalLink, GitBranch } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusPill } from "@/components/ui/status-pill";
import { formatNumber, formatUsd, timeAgo } from "@/lib/format";
import { RelatedRunsTable } from "./run-table";
import { changeHref } from "./utils";

// RelatedChangeCard links the run back to the durable work item it is an
// activation of. The summary may be absent when the change index has no record.
export function RelatedChangeCard({ change, run }) {
  const changeID = change?.change_id || run?.change_id;
  if (!changeID) return null;

  return (
    <Card>
      <CardHeader
        icon={<GitBranch />}
        title="Related change"
        description="The durable work item this run is an activation of."
        actions={change?.status ? <StatusPill kind="change" state={change.status} size="xs" /> : null}
      />
      <CardBody>
        <a
          href={changeHref(changeID)}
          className="block break-all font-mono text-[11.5px] leading-relaxed text-primary hover:underline"
          title={changeID}
        >
          {changeID}
        </a>
        {change ? (
          <dl className="mt-2 grid grid-cols-2 gap-x-3 gap-y-2 text-[11px]">
            <div>
              <dt className="text-[10px] uppercase tracking-[0.08em] text-muted-foreground">Attempts</dt>
              <dd className="tabular mt-0.5 text-foreground">{formatNumber(change.attempts)}</dd>
            </div>
            <div>
              <dt className="text-[10px] uppercase tracking-[0.08em] text-muted-foreground">Updated</dt>
              <dd className="mt-0.5 text-foreground" title={change.updated_at}>
                {timeAgo(change.updated_at)}
              </dd>
            </div>
            <div>
              <dt className="text-[10px] uppercase tracking-[0.08em] text-muted-foreground">Tokens</dt>
              <dd className="tabular mt-0.5 text-foreground">
                {Number(change.total_tokens) > 0 ? formatNumber(change.total_tokens) : "Not reported"}
              </dd>
            </div>
            <div>
              <dt className="text-[10px] uppercase tracking-[0.08em] text-muted-foreground">Cost</dt>
              <dd className="tabular mt-0.5 text-foreground">
                {Number(change.total_cost_usd) > 0 ? formatUsd(change.total_cost_usd) : "Not reported"}
              </dd>
            </div>
          </dl>
        ) : (
          <p className="mt-1.5 text-[11px] leading-relaxed text-muted-foreground">
            The change summary is not present in this response; the link resolves through the change detail screen.
          </p>
        )}
      </CardBody>
    </Card>
  );
}

// PreviewsCard only appears when the run recorded review ingress URLs. The links
// are evidence of a deliberate exposure and may no longer be reachable.
export function PreviewsCard({ previews = [] }) {
  if (!Array.isArray(previews) || previews.length === 0) return null;
  return (
    <Card>
      <CardHeader
        icon={<ExternalLink />}
        title="Review previews"
        description="Ingress URLs published for human review from the run sandbox."
        actions={
          <Badge variant="muted" size="xs">
            {previews.length}
          </Badge>
        }
      />
      <CardBody className="space-y-1.5">
        {previews.map((preview) => (
          <a
            key={`${preview.port}-${preview.url}`}
            href={preview.url}
            target="_blank"
            rel="noreferrer noopener"
            className="flex items-center gap-2 rounded-lg border border-border bg-surface-raised px-2.5 py-1.5 text-[11px] transition-colors hover:border-border-strong"
          >
            <Badge variant="info" size="xs">
              :{preview.port}
            </Badge>
            <span className="min-w-0 flex-1 truncate font-mono text-foreground" title={preview.url}>
              {preview.url}
            </span>
            <ExternalLink aria-hidden="true" className="h-3 w-3 shrink-0 text-muted-foreground" />
          </a>
        ))}
        <p className="text-[10.5px] leading-relaxed text-muted-foreground">
          Previews are a deliberate exposure of the sandbox; availability after the run is not guaranteed.
        </p>
      </CardBody>
    </Card>
  );
}

export function RelatedRunsCard({ runs = [] }) {
  return (
    <Card>
      <CardHeader
        icon={<GitBranch />}
        title="Related runs"
        description="Other activations in the same change lineage."
        actions={
          <Badge variant="muted" size="xs">
            {runs.length}
          </Badge>
        }
      />
      <CardBody padded={false}>
        {runs.length > 0 ? (
          <RelatedRunsTable runs={runs} />
        ) : (
          <EmptyState
            compact
            icon={<GitBranch />}
            title="No related runs"
            description="This run is not part of a multi-attempt change lineage."
          />
        )}
      </CardBody>
    </Card>
  );
}
