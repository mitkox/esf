import clsx from "clsx";
import { ShieldAlert, ShieldCheck, ShieldOff } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { TONE_TEXT } from "@/components/ui/tones";
import { formatDuration, formatNumber, severityTone, timeAgo, titleCase } from "@/lib/format";
import { durationNS, securityMeta } from "./utils";

function Section({ title, description, badge, children }) {
  return (
    <div className="border-t border-border px-4 py-3 first:border-t-0 sm:px-5">
      <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-1">
        <div className="min-w-0">
          <h3 className="text-[11.5px] font-semibold text-foreground">{title}</h3>
          {description ? <p className="mt-0.5 text-[10.5px] leading-relaxed text-muted-foreground">{description}</p> : null}
        </div>
        {badge ? <div className="flex shrink-0 flex-wrap items-center gap-1.5">{badge}</div> : null}
      </div>
      <div className="mt-2">{children}</div>
    </div>
  );
}

function severityVariant(value) {
  const tone = severityTone(value);
  return tone === "muted" ? "muted" : tone;
}

function ProbeObservation({ label, observation }) {
  if (!observation) return null;
  const decided = observation.decided !== false;
  const meta = !decided
    ? { tone: "muted", label: "Undecided" }
    : observation.reachable
      ? { tone: "warning", label: "Reachable" }
      : { tone: "success", label: "Blocked" };

  return (
    <div className="min-w-0 rounded-lg border border-border bg-surface-raised px-2.5 py-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">{label}</span>
        <Badge variant={meta.tone} size="xs" dot>
          {meta.label}
        </Badge>
      </div>
      <p className="mt-1 truncate font-mono text-[10.5px] text-foreground" title={observation.url}>
        {observation.url || "—"}
      </p>
      {observation.detail ? (
        <p className="mt-0.5 break-words font-mono text-[10px] text-muted-foreground">{observation.detail}</p>
      ) : null}
      {!decided ? (
        <p className="mt-0.5 text-[10px] leading-relaxed text-muted-foreground">
          The probe could not decide reachability; undecided probes never fail a run.
        </p>
      ) : null}
    </div>
  );
}

function HardeningStrip({ hardening }) {
  const items = [
    { label: "Tripped", value: hardening.tripped ? "Yes" : "No", tone: hardening.tripped ? "danger" : "success" },
    { label: "Findings", value: formatNumber(hardening.findings || 0), tone: Number(hardening.findings) > 0 ? "warning" : "success" },
    { label: "Egress violations", value: formatNumber(hardening.egress_violations || 0), tone: Number(hardening.egress_violations) > 0 ? "danger" : "success" },
    { label: "Gate checked", value: hardening.gate_checked ? "Yes" : "No", tone: hardening.gate_checked ? "success" : "warning" },
    { label: "Gate self-mod", value: hardening.gate_allowed ? "Allowed" : "Denied", tone: hardening.gate_allowed ? "warning" : "muted" },
  ];
  if (hardening.max_severity) {
    items.push({ label: "Max severity", value: titleCase(hardening.max_severity), tone: severityTone(hardening.max_severity) });
  }

  return (
    <div className="grid grid-cols-2 gap-px overflow-hidden rounded-lg border border-border bg-border sm:grid-cols-3 lg:grid-cols-6">
      {items.map((item) => (
        <div key={item.label} className="min-w-0 bg-surface px-2.5 py-2">
          <p className="truncate text-[9.5px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">{item.label}</p>
          <p className={clsx("tabular mt-0.5 text-[13px] font-semibold leading-5", TONE_TEXT[item.tone] || TONE_TEXT.muted)}>
            {item.value}
          </p>
        </div>
      ))}
    </div>
  );
}

// SecurityCard is the per-run hardening evidence. It reports exactly what the
// egress probe, behavior scan and gate-integrity check recorded, including
// undecided probes and missing alert delivery.
export function SecurityCard({ run, security }) {
  const hardening = run?.hardening;
  const meta = securityMeta(hardening, run?.factory_result);
  const probe = security?.egress_probe;
  const behavior = security?.behavior;
  const gate = security?.gate_integrity;
  const alerts = Array.isArray(security?.alerts) ? security.alerts : [];
  const findings = Array.isArray(behavior?.findings) ? behavior.findings : [];

  return (
    <Card>
      <CardHeader
        icon={meta.tone === "success" ? <ShieldCheck /> : meta.tone === "muted" ? <ShieldOff /> : <ShieldAlert />}
        title="Security"
        description="Hardening evidence recorded for this run: egress probe, behavior scan, gate integrity and alerts."
        actions={
          <Badge
            variant={meta.tone === "muted" ? "muted" : meta.tone}
            size="xs"
            dot
            title={meta.detail}
          >
            {meta.label}
          </Badge>
        }
      />
      <CardBody padded={false}>
        {!security ? (
          <EmptyState
            compact
            icon={<ShieldOff />}
            title="Hardening not measured"
            description="No hardening evidence is recorded for this run. This is not a clean result; it means the security controls did not report."
          />
        ) : (
          <>
            {hardening ? (
              <div className="px-4 pb-3 pt-3 sm:px-5">
                <HardeningStrip hardening={hardening} />
                {hardening.gate_modified?.length ? (
                  <p className="mt-2 text-[11px] leading-relaxed text-danger">
                    Modified gate paths: <span className="font-mono">{hardening.gate_modified.join(", ")}</span>
                  </p>
                ) : null}
              </div>
            ) : null}

            {probe ? (
              <Section
                title="Egress probe"
                description={`Policy ${probe.policy || "—"} · probed ${timeAgo(probe.probed_at)}${
                  probe.duration ? ` · ${durationNS(probe.duration)}` : ""
                }`}
                badge={
                  <Badge variant={probe.expect_deny ? "info" : "muted"} size="xs">
                    {probe.expect_deny ? "Internet expected blocked" : "Internet expected allowed"}
                  </Badge>
                }
              >
                <div className="grid gap-2 sm:grid-cols-2">
                  <ProbeObservation label="Canary" observation={probe.canary} />
                  <ProbeObservation label="Metadata" observation={probe.metadata} />
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[10.5px] text-muted-foreground">
                  <span>
                    Agent identity{" "}
                    <span className="font-mono text-foreground">
                      {probe.agent_uid !== undefined && probe.agent_uid !== null ? probe.agent_uid : "unknown"}
                      {probe.agent_user ? ` (${probe.agent_user})` : ""}
                    </span>
                  </span>
                  {probe.probed_at ? <span title={probe.probed_at}>Probed {timeAgo(probe.probed_at)}</span> : null}
                  {probe.duration ? <span className="tabular">Duration {durationNS(probe.duration)}</span> : null}
                </div>
                {probe.violations?.length ? (
                  <ul className="mt-2 space-y-1 rounded-lg border border-danger/30 bg-danger/5 px-2.5 py-2 text-[11px] text-danger">
                    {probe.violations.map((violation) => (
                      <li key={violation} className="break-words">
                        {violation}
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="mt-2 text-[11px] text-muted-foreground">No egress invariant violations recorded.</p>
                )}
                {probe.notes?.length ? (
                  <ul className="mt-1.5 space-y-0.5 text-[10.5px] leading-relaxed text-muted-foreground">
                    {probe.notes.map((note) => (
                      <li key={note} className="break-words">
                        {note}
                      </li>
                    ))}
                  </ul>
                ) : null}
                {probe.raw ? (
                  <details className="mt-2">
                    <summary className="cursor-pointer text-[11px] font-medium text-primary">Show raw probe output</summary>
                    <pre className="scroll-thin mt-1.5 max-h-48 overflow-auto rounded-lg border border-border bg-surface-raised px-2.5 py-2 font-mono text-[10.5px] leading-relaxed text-foreground">
                      {probe.raw}
                    </pre>
                  </details>
                ) : null}
              </Section>
            ) : null}

            {behavior || security.behavior_error ? (
              <Section
                title="Behavior scan"
                description="Deterministic rules over the agent output streams."
                badge={
                  behavior ? (
                    <Badge variant={behavior.tripped ? "danger" : findings.length > 0 ? "warning" : "success"} size="xs" dot>
                      {behavior.tripped ? "Tripped" : findings.length > 0 ? "Findings" : "Clean"}
                    </Badge>
                  ) : (
                    <Badge variant="warning" size="xs">
                      Scan error
                    </Badge>
                  )
                }
              >
                {security.behavior_error ? (
                  <p className="mb-2 break-words text-[11px] text-warning">{security.behavior_error}</p>
                ) : null}
                {behavior ? (
                  <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[10.5px] text-muted-foreground">
                    <span>
                      Ruleset <span className="font-mono text-foreground">{behavior.ruleset_version || "—"}</span>
                    </span>
                    <span>
                      Scanned lines <span className="tabular text-foreground">{formatNumber(behavior.scanned_lines || 0)}</span>
                    </span>
                    {behavior.max_severity ? (
                      <span className="inline-flex items-center gap-1">
                        Max severity{" "}
                        <Badge variant={severityVariant(behavior.max_severity)} size="xs">
                          {titleCase(behavior.max_severity)}
                        </Badge>
                      </span>
                    ) : null}
                  </div>
                ) : null}
                {findings.length > 0 ? (
                  <ul className="mt-2 divide-y divide-border overflow-hidden rounded-lg border border-border">
                    {findings.map((finding, index) => (
                      <li key={`${finding.rule_id}-${index}`} className="px-2.5 py-2">
                        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[10.5px]">
                          <Badge variant={severityVariant(finding.severity)} size="xs">
                            {titleCase(finding.severity)}
                          </Badge>
                          <span className="font-mono text-foreground">{finding.rule_id}</span>
                          <span className="text-muted-foreground">{finding.category}</span>
                          <span className="ml-auto font-mono text-muted-foreground">
                            {finding.stream}:{finding.line}
                          </span>
                        </div>
                        {finding.excerpt ? (
                          <p className="mt-1 break-words font-mono text-[10px] leading-relaxed text-muted-foreground" title={finding.excerpt}>
                            {finding.excerpt}
                          </p>
                        ) : null}
                      </li>
                    ))}
                  </ul>
                ) : behavior ? (
                  <p className="mt-2 text-[11px] text-muted-foreground">
                    {behavior.tripped
                      ? "The behavior scan tripped but recorded no finding rows."
                      : "No behavior findings recorded."}
                  </p>
                ) : null}
              </Section>
            ) : null}

            {gate ? (
              <Section
                title="Gate integrity"
                description="Whether the agent's patch touched the deterministic gates that judge it."
                badge={
                  <Badge
                    variant={!gate.checked ? "muted" : gate.modified?.length ? "danger" : "success"}
                    size="xs"
                    dot
                  >
                    {!gate.checked ? "Not checked" : gate.modified?.length ? "Modified" : "Unmodified"}
                  </Badge>
                }
              >
                <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[10.5px] text-muted-foreground">
                  <span>
                    Method <span className="font-mono text-foreground">{gate.method || "—"}</span>
                  </span>
                  <span>
                    Self-modification{" "}
                    <span className={gate.allowed ? "text-warning" : "text-foreground"}>
                      {gate.allowed ? "allowed" : "denied"}
                    </span>
                  </span>
                </div>
                {gate.gate_paths?.length ? (
                  <div className="mt-1.5 flex flex-wrap gap-1">
                    {gate.gate_paths.map((path) => (
                      <span
                        key={path}
                        className="rounded border border-border bg-surface-raised px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground"
                      >
                        {path}
                      </span>
                    ))}
                  </div>
                ) : null}
                {gate.modified?.length ? (
                  <p className="mt-1.5 break-words text-[11px] text-danger">
                    Modified: <span className="font-mono">{gate.modified.join(", ")}</span>
                  </p>
                ) : (
                  <p className="mt-1.5 text-[11px] text-muted-foreground">No configured gate paths were modified.</p>
                )}
              </Section>
            ) : null}

            {alerts.length > 0 ? (
              <Section
                title="Security alerts"
                description="Delivery is best effort; the record keeps undelivered alerts visible."
                badge={
                  <Badge variant={alerts.every((alert) => alert.delivered) ? "success" : "warning"} size="xs">
                    {alerts.length} alert{alerts.length === 1 ? "" : "s"}
                  </Badge>
                }
              >
                <ul className="divide-y divide-border overflow-hidden rounded-lg border border-border">
                  {alerts.map((alert, index) => (
                    <li key={`${alert.kind}-${index}`} className="px-2.5 py-2">
                      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[10.5px]">
                        <Badge variant={severityVariant(alert.severity)} size="xs">
                          {titleCase(alert.severity)}
                        </Badge>
                        <span className="font-mono text-foreground">{alert.kind}</span>
                        <Badge variant={alert.delivered ? "success" : "danger"} size="xs" dot>
                          {alert.delivered ? "Delivered" : "Not delivered"}
                        </Badge>
                        <span className="ml-auto text-[10px] text-muted-foreground" title={alert.at}>
                          {timeAgo(alert.at)}
                        </span>
                      </div>
                      {alert.summary ? <p className="mt-1 break-words text-[11px] text-muted-foreground">{alert.summary}</p> : null}
                      {alert.error ? <p className="mt-0.5 break-words font-mono text-[10px] text-danger">{alert.error}</p> : null}
                    </li>
                  ))}
                </ul>
              </Section>
            ) : null}

            {!probe && !behavior && !gate && alerts.length === 0 ? (
              <EmptyState
                compact
                icon={<ShieldOff />}
                title="No hardening sections recorded"
                description="The hardening evidence block exists but contains no egress, behavior, gate or alert records."
              />
            ) : null}
          </>
        )}
      </CardBody>
    </Card>
  );
}
