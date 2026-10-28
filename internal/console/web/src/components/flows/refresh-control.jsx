import { useEffect, useState } from "react";
import clsx from "clsx";
import { RefreshCw } from "lucide-react";
import { timeAgo } from "@/lib/format";
import { Button } from "@/components/ui/button";

// RefreshControl pairs the page-level refresh action with the honest evidence
// timestamp. It re-renders the relative label on a slow timer so "2m ago"
// never freezes while a poll is in flight.
export function RefreshControl({ updatedAt, loading = false, onRefresh, className }) {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 15000);
    return () => window.clearInterval(timer);
  }, []);

  return (
    <div className={clsx("flex items-center gap-2", className)}>
      <span className="hidden text-[11px] tabular text-muted-foreground sm:inline">
        {updatedAt ? `Updated ${timeAgo(updatedAt, now)}` : "Not yet refreshed"}
      </span>
      <Button
        variant="secondary"
        size="sm"
        onClick={onRefresh}
        aria-label="Refresh this view"
        aria-busy={loading || undefined}
        icon={<RefreshCw className={loading ? "animate-spin" : undefined} />}
      >
        Refresh
      </Button>
    </div>
  );
}
