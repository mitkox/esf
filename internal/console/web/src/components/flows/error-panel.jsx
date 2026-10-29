import clsx from "clsx";
import { RefreshCw, TriangleAlert } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

// ErrorPanel is the shared failure state for screens that could not load their
// primary evidence. It keeps the server's message and HTTP/code visible so an
// operator can tell a missing store from an unreachable console.
export function ErrorPanel({ error, onRetry, title = "This view could not be loaded", retryLabel = "Retry", className }) {
  const message = error?.message || "The console did not return a response.";
  const status = error?.status ? `HTTP ${error.status}` : error?.code === "network" ? "Network error" : "";
  return (
    <Card className={clsx("p-5", className)} role="alert">
      <div className="flex flex-col items-start gap-3 sm:flex-row sm:items-start">
        <div className="grid h-9 w-9 shrink-0 place-items-center rounded-lg border border-danger/25 bg-danger/10 text-danger">
          <TriangleAlert aria-hidden="true" className="h-4 w-4" />
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-[13px] font-semibold text-foreground">{title}</p>
          <p className="mt-1 break-words text-[12px] leading-relaxed text-muted-foreground">{message}</p>
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            {status ? (
              <Badge variant="muted" size="xs">
                {status}
              </Badge>
            ) : null}
            {error?.code ? (
              <span className="mono rounded border border-border bg-surface-raised px-1.5 py-0.5 text-[10px] text-muted-foreground">
                {error.code}
              </span>
            ) : null}
          </div>
        </div>
        {onRetry ? (
          <Button variant="secondary" size="sm" onClick={onRetry} icon={<RefreshCw />}>
            {retryLabel}
          </Button>
        ) : null}
      </div>
    </Card>
  );
}
