import clsx from "clsx";
import { ArrowRight } from "lucide-react";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { BreakdownBars } from "@/components/charts/breakdown-bars";

// BreakdownCard is the shared shell for the ranked harness / repository lists.
// The bars answer "who is doing the work"; success rate and p50 duration stay
// as exact text so a glance never has to estimate a number.
export function BreakdownCard({
  title,
  description,
  icon,
  items = [],
  labelOf,
  emptyLabel,
  note,
  extras,
  href,
  hrefLabel,
  className,
}) {
  return (
    <Card className={clsx("flex h-full min-w-0 flex-col", className)}>
      <CardHeader icon={icon} title={title} description={description} />
      <CardBody className="flex-1">
        <BreakdownBars items={items} labelOf={labelOf} emptyLabel={emptyLabel} />
      </CardBody>
      <CardFooter className="mt-auto">
        {extras ? <span className="min-w-0 flex-1 truncate">{extras}</span> : null}
        <span className={clsx("min-w-0 text-[10px] text-muted-foreground/80", extras && "hidden lg:inline")}>{note}</span>
        {href ? (
          <Button href={href} variant="ghost" size="xs" iconRight={<ArrowRight />} className="ml-auto">
            {hrefLabel || "Open"}
          </Button>
        ) : null}
      </CardFooter>
    </Card>
  );
}
