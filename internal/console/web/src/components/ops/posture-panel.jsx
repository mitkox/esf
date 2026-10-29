import { ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { StatusPill } from "@/components/ui/status-pill";
import { formatNumber } from "@/lib/format";
import { BoolChip } from "./chips";
import { asNumber, asObject, has, textOr } from "./data";
import { Notice } from "./notice";

function PostureTile({ label, chip, note }) {
  return (
    <div className="min-w-0 bg-surface px-4 py-3.5">
      <div className="flex flex-wrap items-center justify-between gap-x-2 gap-y-1">
        <p className="text-[11px] font-medium leading-4 text-foreground">{label}</p>
        {chip}
      </div>
      <p className="mt-1 break-words text-[11px] leading-snug text-muted-foreground">{note}</p>
    </div>
  );
}

// PosturePanel is deliberately a configuration view, not a health score: each
// control says whether the operator turned it on and what turning it off means.
export function PosturePanel({ posture, alertsUndelivered, inspected, onViewAlerts }) {
  const source = asObject(posture);
  const configured = Object.keys(source).length > 0;
  const undelivered = asNumber(alertsUndelivered);

  if (!configured) {
    return (
      <Card>
        <CardHeader
          icon={<ShieldCheck />}
          title="Defense-in-depth posture"
          description="Operator-configured hardening controls from the factory configuration."
        />
        <CardBody>
          <Notice tone="warning" title="Posture not reported">
            This security response did not include a posture block, so no control state is claimed.
          </Notice>
        </CardBody>
      </Card>
    );
  }

  const inspectedCount = asNumber(inspected);
  const canaryURL = textOr(source.egress_canary_url, "");
  const canaryConfigured = has(source, "egress_canary_url") ? Boolean(canaryURL) : undefined;
  const bool = (key) => (has(source, key) ? source[key] : undefined);
  const severity = textOr(source.trip_severity, "");
  const controls = [
    {
      label: "Egress probing",
      chip: (
        <BoolChip
          value={bool("egress_probe_enabled")}
          onLabel="Enabled"
          offLabel="Disabled"
          toneOn="success"
          toneOff="warning"
        />
      ),
      note: "Canary and metadata probes run inside each sandbox to measure the network boundary.",
    },
    {
      label: "Behavior monitoring",
      chip: (
        <BoolChip
          value={bool("behavior_monitor_enabled")}
          onLabel="Enabled"
          offLabel="Disabled"
          toneOn="success"
          toneOff="warning"
        />
      ),
      note: "Agent commands are scanned against the hardening rule set for every run.",
    },
    {
      label: "Trip severity",
      chip: severity ? (
        <StatusPill kind="severity" state={severity} />
      ) : (
        <BoolChip value={undefined} unknownLabel="Not reported" />
      ),
      note: severity ? `Findings at ${severity} or above quarantine the run.` : "No trip threshold was reported.",
    },
    {
      label: "Require non-root agent",
      chip: (
        <BoolChip
          value={bool("require_non_root")}
          onLabel="Enforced"
          offLabel="Not enforced"
          toneOn="success"
          toneOff="warning"
        />
      ),
      note: "When not enforced, the agent may run as uid 0 inside the sandbox.",
    },
    {
      label: "Open egress acknowledgement",
      chip: (
        <BoolChip
          value={bool("acknowledge_open_egress")}
          onLabel="Acknowledged"
          offLabel="Strict"
          toneOn="warning"
          toneOff="success"
        />
      ),
      note: "Acknowledged means open egress is an accepted posture; violations are still recorded.",
    },
    {
      label: "Gate self-modification",
      chip: (
        <BoolChip
          value={bool("allow_gate_self_modification")}
          onLabel="Allowed"
          offLabel="Blocked"
          toneOn="danger"
          toneOff="success"
        />
      ),
      note: "When blocked, a patch that edits its own verification gates quarantines the run.",
    },
    {
      label: "Alert webhook",
      chip: (
        <BoolChip
          value={bool("alert_webhook_configured")}
          onLabel="Configured"
          offLabel="Not configured"
          toneOn="success"
          toneOff="danger"
        />
      ),
      note: "Without a webhook, alerts are recorded in evidence but never delivered.",
    },
    {
      label: "Egress canary target",
      chip: (
        <BoolChip
          value={canaryConfigured}
          onLabel="Configured"
          offLabel="Not configured"
          unknownLabel="Not reported"
          toneOn="info"
          toneOff="warning"
        />
      ),
      note: canaryURL
        ? canaryURL
        : canaryConfigured === false
          ? "No canary URL is configured, so egress probing has no target."
          : "The canary target was not reported.",
    },
  ];


  return (
    <Card>
      <CardHeader
        icon={<ShieldCheck />}
        title="Defense-in-depth posture"
        description="Operator-configured hardening controls. This is configuration, not a per-run measurement."
        actions={<Badge variant="security" size="sm" dot>Hardening posture</Badge>}
      />
      <div className="grid gap-px border-t border-border bg-border sm:grid-cols-2 xl:grid-cols-4">
        {controls.map((control) => (
          <PostureTile key={control.label} label={control.label} chip={control.chip} note={control.note} />
        ))}
      </div>
      <CardFooter>
        <span>
          {inspectedCount === null ? "Coverage not reported" : `${formatNumber(inspectedCount)} runs inspected in this window.`}
        </span>
        {undelivered !== null && undelivered > 0 && onViewAlerts ? (
          <button
            type="button"
            onClick={onViewAlerts}
            className="-mx-1 min-h-[24px] rounded-md px-1 py-1 font-medium text-warning underline-offset-4 hover:underline focus-visible:outline-offset-4 sm:ml-auto"
          >
            View {formatNumber(undelivered)} undelivered alert{undelivered === 1 ? "" : "s"}
          </button>
        ) : null}
      </CardFooter>
    </Card>
  );
}
