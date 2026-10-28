import { ScrollText } from "lucide-react";
import { CopyButton } from "@/components/ui/copy-button";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";

// TaskPanel shows the bounded task preview recorded with the change. The full
// task text is intentionally not served by this endpoint.
export function TaskPanel({ change }) {
  const preview = change?.task_preview || "";
  const hash = change?.task_hash || "";
  const truncated = preview.endsWith("…");

  return (
    <Card className="overflow-hidden">
      <CardHeader
        icon={<ScrollText />}
        title="Task specification"
        description="Bounded preview recorded with the change. Full task text stays in the run evidence."
        actions={truncated ? <span className="text-[10.5px] text-muted-foreground">Truncated preview</span> : null}
      />
      <CardBody>
        {preview ? (
          <p className="max-w-4xl whitespace-pre-wrap break-words text-[12.5px] leading-relaxed text-foreground">{preview}</p>
        ) : (
          <EmptyState
            compact
            icon={<ScrollText aria-hidden="true" />}
            title="No task preview recorded"
            description="The change artifact carries a task hash but no preview text. Use the run evidence for the full task specification."
          />
        )}
      </CardBody>
      <CardFooter>
        <span className="shrink-0">Task hash</span>
        {hash ? (
          <>
            <span className="mono min-w-0 truncate text-[10.5px] text-muted-foreground" title={hash}>
              {hash}
            </span>
            <CopyButton value={hash} label="task hash" size="xs" className="shrink-0" />
          </>
        ) : (
          <span className="text-muted-foreground">Not reported</span>
        )}
      </CardFooter>
    </Card>
  );
}
