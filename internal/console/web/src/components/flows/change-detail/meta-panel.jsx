import { Layers } from "lucide-react";
import { formatDateTime, repoLabel, shortSha } from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { DefinitionList } from "@/components/flows/definition-list";
import { latestRun } from "@/components/flows/helpers";

// ChangeMetaPanel is the identity record: repository, revisions, hashes and
// timestamps as recorded in the durable change artifact.
export function ChangeMetaPanel({ change, runs }) {
  const latest = latestRun(runs);
  const rows = [
    {
      label: "Repository",
      value: change?.repository ? repoLabel(change.repository) : "Not reported",
      title: change?.repository || undefined,
      copy: change?.repository || "",
    },
    { label: "Scope", value: change?.scope || "Not reported" },
    {
      label: "Requested rev",
      value: change?.requested_revision ? shortSha(change.requested_revision) : "Not reported",
      title: change?.requested_revision || undefined,
      mono: true,
      copy: change?.requested_revision || "",
    },
    {
      label: "Baseline rev",
      value: latest?.baseline_sha ? shortSha(latest.baseline_sha) : "Not reported",
      title: latest?.baseline_sha || undefined,
      mono: true,
    },
    {
      label: "Last run",
      value: change?.last_run_id || "No runs",
      title: change?.last_run_id || undefined,
      mono: true,
      copy: change?.last_run_id || "",
    },
    {
      label: "Task hash",
      value: change?.task_hash ? shortSha(change.task_hash, 12) : "Not reported",
      title: change?.task_hash || undefined,
      mono: true,
      copy: change?.task_hash || "",
    },
    { label: "Created", value: formatDateTime(change?.created_at), title: change?.created_at || undefined },
    { label: "Updated", value: formatDateTime(change?.updated_at), title: change?.updated_at || undefined },
  ];

  return (
    <Card className="overflow-hidden">
      <CardHeader
        icon={<Layers />}
        title="Change record"
        description="Durable identities and timestamps from the change artifact."
        actions={
          <Badge variant={change?.has_evidence ? "success" : "warning"} size="sm" dot>
            {change?.has_evidence ? "Evidence retained" : "No evidence"}
          </Badge>
        }
      />
      <CardBody className="py-2">
        <DefinitionList rows={rows} />
      </CardBody>
      <CardFooter>
        <span className="tabular">
          {Array.isArray(runs) ? runs.length : 0} run{(Array.isArray(runs) ? runs.length : 0) === 1 ? "" : "s"} resolved ·{" "}
          {Array.isArray(change?.activations) ? change.activations.length : 0} activation
          {Array.isArray(change?.activations) && change.activations.length === 1 ? "" : "s"} recorded
        </span>
      </CardFooter>
    </Card>
  );
}
