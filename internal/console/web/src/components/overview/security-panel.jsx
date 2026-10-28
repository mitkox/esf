import clsx from "clsx";
import { ArrowRight, ShieldCheck } from "lucide-react";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { Badge, StatusDot } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TONE_TEXT } from "@/components/ui/tones";
import { formatNumber, severityTone } from "@/lib/format";

function Stat({ label, value, hint, tone = "muted" }) {
  const number = Number(value) || 0;
  return (
    <div className="min-w-0" title={hint}>
      <dt className="truncate text-[9px] font-semibold uppercase leading-4 tracking-[0.1em] text-muted-foreground">
        {label}
      </dt>
      <dd
        className={clsx(
          "tabular mt-0.5 text-[15px] font-semibold leading-5",
          number > 0 && tone !== "muted" ? TONE_TEXT[tone] : "text-foreground",
        )}
      >
        {formatNumber(number)}
      </dd>
    </div>
  );
}

export function SecurityPanel({ security, className }) {
  const data = security || {};
  const stops = Number(data.stops_in_window) || 0;
  const findings = Number(data.runs_with_findings) || 0;
  const rules = Array.isArray(data.top_rules) ? data.top_rules : [];
  const inspected = Number(data.runs_scanned) || 0;

  return (
    <Card className={clsx("flex h-full min-w-0 flex-col", className)}>
      <CardHeader
        icon={<ShieldCheck />}
        title="Security posture"
        description="Hardening, egress and gate-integrity evidence"
        actions={
          <Badge variant={stops > 0 ? "danger" : "success"} size="xs" dot>
            {stops > 0 ? `${formatNumber(stops)} stop${stops === 1 ? "" : "s"}` : "No stops"}
          </Badge>
        }
      />
      <CardBody className="flex flex-1 flex-col">
        <div className="flex items-start justify-between gap-4 border-b border-border/70 pb-3">
          <div className="min-w-0">
            <p className="text-[9px] font-semibold uppercase leading-4 tracking-[0.1em] text-muted-foreground">
              Stops in window
            </p>
            <p
              className={clsx(
                "tabular mt-1 text-[26px] font-semibold leading-none",
                stops > 0 ? "text-danger" : "text-foreground",
              )}
            >
              {formatNumber(stops)}
            </p>
            <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
              {stops > 0 ? "Security terminal states in this window" : "No security terminal state in this window"}
            </p>
          </div>
          <div className="min-w-0 text-right">
            <p className="text-[9px] font-semibold uppercase leading-4 tracking-[0.1em] text-muted-foreground">
              With findings
            </p>
            <p
              className={clsx(
                "tabular mt-1 text-[26px] font-semibold leading-none",
                findings > 0 ? "text-warning" : "text-foreground",
              )}
            >
              {formatNumber(findings)}
            </p>
            <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
              Across {formatNumber(inspected)} inspected runs
            </p>
          </div>
        </div>

        <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2.5">
          <Stat
            label="Runs inspected"
            value={data.runs_scanned}
            hint="Runs scanned by the cross-run threat report (full evidence store)."
          />
          <Stat
            label="Tripped"
            value={data.tripped}
            tone="danger"
            hint="Runs tripped by the behavior monitor (full evidence store)."
          />
          <Stat
            label="Blocked"
            value={data.blocked}
            tone="danger"
            hint="Runs blocked by the agent harness (full evidence store)."
          />
          <Stat
            label="Gate tampered"
            value={data.gate_tampered}
            tone="danger"
            hint="Runs where the verification gate appears modified (full evidence store)."
          />
          <Stat
            label="Egress violations"
            value={data.egress_violations}
            tone="danger"
            hint="Recorded egress violations (full evidence store)."
          />
          <Stat
            label="Alerts undelivered"
            value={data.alerts_undelivered}
            tone="warning"
            hint="Security alerts that could not be delivered (full evidence store)."
          />
        </dl>

        <div className="mt-3 border-t border-border/70 pt-3">
          <p className="text-[9px] font-semibold uppercase leading-4 tracking-[0.1em] text-muted-foreground">
            Top rules
          </p>
          {rules.length ? (
            <ul className="mt-1.5 space-y-1">
              {rules.map((rule, index) => (
                <li key={rule.rule_id || index} className="flex items-center gap-2 text-[11px] leading-4">
                  <StatusDot tone={severityTone(rule.severity)} size="xs" />
                  <span className="mono min-w-0 flex-1 truncate text-foreground" title={rule.rule_id}>
                    {rule.rule_id || "unnamed rule"}
                  </span>
                  {rule.category ? (
                    <span className="hidden shrink-0 text-muted-foreground sm:inline">{rule.category}</span>
                  ) : null}
                  <span className="tabular shrink-0 text-muted-foreground">
                    {formatNumber(rule.findings)} finding{(Number(rule.findings) || 0) === 1 ? "" : "s"}
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="mt-1 text-[11px] leading-4 text-muted-foreground">
              No hardening rule fired across the inspected runs.
            </p>
          )}
        </div>
      </CardBody>
      <CardFooter className="mt-auto">
        <span className="min-w-0 truncate">Threat report spans the full store · stops are counted in the window</span>
        <Button href="#/security" variant="ghost" size="xs" iconRight={<ArrowRight />} className="ml-auto">
          Security report
        </Button>
      </CardFooter>
    </Card>
  );
}
