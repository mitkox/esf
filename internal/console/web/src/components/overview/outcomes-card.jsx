import { ChartPie } from "lucide-react";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { OutcomeDonut } from "@/components/charts/outcome-donut";
import { formatNumber } from "@/lib/format";

export function OutcomesCard({ data, className }) {
  const outcomes = Array.isArray(data?.outcomes) ? data.outcomes : [];
  const total = outcomes.reduce((accumulator, outcome) => accumulator + (Number(outcome.count) || 0), 0);
  const windowName = data?.window?.label || "the selected window";

  return (
    <Card className={`flex h-full min-w-0 flex-col ${className || ""}`}>
      <CardHeader
        icon={<ChartPie />}
        title="Outcome mix"
        description="Factory results recorded in this window"
        actions={
          <Badge variant={total > 0 ? "outline" : "muted"} size="xs">
            {formatNumber(total)} results
          </Badge>
        }
      />
      <CardBody className="flex flex-1 flex-col justify-center">
        {total > 0 ? (
          <OutcomeDonut outcomes={outcomes} total={total} />
        ) : (
          <EmptyState
            compact
            icon={<ChartPie aria-hidden="true" className="h-4 w-4" />}
            title="No results in this window"
            description={`No terminal factory results were recorded in ${windowName.toLowerCase()}.`}
          />
        )}
      </CardBody>
      <CardFooter>
        <span className="truncate">Grouped by factory result · shares are of all results returned</span>
        <Button href="#/runs" variant="ghost" size="xs" className="ml-auto">
          Runs
        </Button>
      </CardFooter>
    </Card>
  );
}
