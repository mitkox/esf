import { Fingerprint } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { CopyButton } from "@/components/ui/copy-button";
import { formatDateTime, formatDuration, formatNumber, formatUsd, shortSha, timeAgo, titleCase } from "@/lib/format";
import { changeHref } from "./utils";
import { ReviewIndicator } from "./signals";

function Row({ label, children }) {
  return (
    <div className="min-w-0">
      <dt className="text-[10px] font-medium uppercase tracking-[0.08em] text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 break-words text-[12px] leading-snug text-foreground">{children || "—"}</dd>
    </div>
  );
}

function Mono({ value, title }) {
  if (!value) return "—";
  return (
    <span className="break-all font-mono text-[11px] leading-relaxed" title={title || value}>
      {value}
    </span>
  );
}

function Sha({ value }) {
  if (!value) return "—";
  return (
    <span className="inline-flex max-w-full items-center gap-1">
      <span className="font-mono text-[11px]" title={value}>
        {shortSha(value, 12)}
      </span>
      <CopyButton value={value} label="SHA" size="xs" />
    </span>
  );
}

function TokenValue({ value }) {
  if (value === null || value === undefined) return "Not reported";
  return <span className="tabular">{formatNumber(value)}</span>;
}

function Section({ title, children }) {
  return (
    <section className="min-w-0">
      <h3 className="text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">{title}</h3>
      <dl className="mt-1.5 space-y-2">{children}</dl>
    </section>
  );
}

// RunMetadataCard lays out the identity, input, environment and accounting
// fields recorded in the durable manifest. Missing fields render as "—"
// instead of plausible defaults.
export function RunMetadataCard({ detail }) {
  const run = detail?.run || {};
  const manifest = detail?.manifest || {};
  const task = detail?.task || {};
  const changeID = detail?.change?.change_id || run.change_id;
  const budget = Array.isArray(detail?.budget_exceeded) ? detail.budget_exceeded : [];

  return (
    <Card className="mb-4">
      <CardHeader
        icon={<Fingerprint />}
        title="Run metadata"
        description="Identity, inputs, execution environment and accounting as recorded in the run manifest."
        actions={
          manifest.hardening === null || manifest.hardening === undefined ? (
            <Badge variant="warning" size="xs">
              Hardening not recorded
            </Badge>
          ) : null
        }
      />
      <CardBody className="grid gap-x-8 gap-y-5 sm:grid-cols-2 xl:grid-cols-4">
        <Section title="Identity">
          <Row label="Run ID">
            <span className="inline-flex max-w-full items-center gap-1">
              <Mono value={run.run_id} />
              {run.run_id ? <CopyButton value={run.run_id} label="run ID" size="xs" /> : null}
            </span>
          </Row>
          <Row label="Change">
            {changeID ? (
              <a
                href={changeHref(changeID)}
                className="break-all font-mono text-[11px] text-primary hover:underline"
                title={changeID}
              >
                {changeID}
              </a>
            ) : (
              "Not linked to a change"
            )}
          </Row>
          <Row label="Factory version">
            <Mono value={manifest.factory_version} />
          </Row>
          <Row label="Workflow">
            <Mono value={manifest.workflow_id} />
          </Row>
          <Row label="Workflow run">
            <Mono value={manifest.workflow_run_id} />
          </Row>
          <Row label="Task hash">
            <Sha value={manifest.task_hash || task.task_hash} />
          </Row>
        </Section>

        <Section title="Repository & inputs">
          <Row label="Repository">
            <span className="break-all font-mono text-[11px] leading-relaxed" title={run.repository}>
              {run.repository || "—"}
            </span>
          </Row>
          <Row label="Repository kind">{run.repository_kind || "—"}</Row>
          <Row label="Scope">{run.scope || "—"}</Row>
          <Row label="Requested revision">
            <Sha value={run.requested_revision} />
          </Row>
          <Row label="Baseline SHA">
            <Sha value={run.baseline_sha} />
          </Row>
          <Row label="Resulting SHA">
            <Sha value={run.resulting_sha_if_any} />
          </Row>
        </Section>

        <Section title="Execution environment">
          <Row label="Harness">
            <Mono value={run.agent_harness} />
            {run.agent_version ? <span className="ml-1 text-[10.5px] text-muted-foreground">{run.agent_version}</span> : null}
          </Row>
          <Row label="Verification profile">{run.verification_profile || "—"}</Row>
          <Row label="Sandbox provider">{manifest.sandbox_provider || "—"}</Row>
          <Row label="Sandbox ID">
            <Mono value={run.sandbox_id || manifest.sandbox_id} />
          </Row>
          <Row label="Sandbox template">
            <Mono value={manifest.sandbox_template} />
          </Row>
          <Row label="Cube version">
            <Mono value={manifest.cube_version_if_available} />
          </Row>
          <Row label="Workspace">
            <Mono value={manifest.workspace} />
          </Row>
          <Row label="Workspace digest">
            <Sha value={manifest.workspace_digest} />
          </Row>
          <Row label="Egress policy">
            <Mono value={manifest.egress_policy} />
          </Row>
          <Row label="Egress digest">
            <Sha value={manifest.egress_digest} />
          </Row>
          <Row label="Egress summary">{manifest.egress_summary || "—"}</Row>
          <Row label="Model digest">
            <Sha value={manifest.model_digest} />
          </Row>
          <Row label="Resource digest">
            <Sha value={manifest.resource_digest} />
          </Row>
        </Section>

        <Section title="Lifecycle & accounting">
          <Row label="Started">
            <span title={run.started_at || undefined}>
              {formatDateTime(run.started_at)}
              {run.started_at ? <span className="ml-1 text-muted-foreground">({timeAgo(run.started_at)})</span> : null}
            </span>
          </Row>
          <Row label="Completed">
            <span title={run.completed_at || undefined}>
              {run.completed_at ? formatDateTime(run.completed_at) : "Not completed"}
            </span>
          </Row>
          <Row label="Duration">{formatDuration(run.duration_ms)}</Row>
          <Row label="Tokens in">
            <TokenValue value={run.tokens_in} />
          </Row>
          <Row label="Tokens out">
            <TokenValue value={run.tokens_out} />
          </Row>
          <Row label="Inference cost">
            {run.inference_cost === null || run.inference_cost === undefined ? "Not reported" : formatUsd(run.inference_cost)}
          </Row>
          <Row label="Review">
            <ReviewIndicator run={run} />
          </Row>
          {run.review_note ? <Row label="Reviewer note">{run.review_note}</Row> : null}
          {run.review_error ? (
            <Row label="Review error">
              <span className="text-warning">{run.review_error}</span>
            </Row>
          ) : null}
          <Row label="Budget">
            {!detail?.manifest ? (
              "No budget evidence recorded"
            ) : budget.length > 0 ? (
              <span className="text-warning">Exceeded: {budget.join(", ")}</span>
            ) : (
              "No budget exceedance recorded"
            )}
          </Row>
          {manifest.preview_result ? <Row label="Preview result">{titleCase(manifest.preview_result)}</Row> : null}
          {manifest.suspend_result ? <Row label="Suspend result">{titleCase(manifest.suspend_result)}</Row> : null}
        </Section>
      </CardBody>
    </Card>
  );
}
