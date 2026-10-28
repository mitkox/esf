import clsx from "clsx";
import {
  BrainCircuit,
  Container,
  Database,
  FolderTree,
  Gauge,
  Info,
  Layers,
  ListChecks,
  Lock,
  Network,
  RefreshCw,
  ScanSearch,
  Server,
  ShieldCheck,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { PageHeader } from "@/components/ui/page-header";
import { StatusPill } from "@/components/ui/status-pill";
import { Skeleton, TableSkeleton } from "@/components/ui/skeleton";
import { BoolChip } from "@/components/ops/chips";
import { ConfigTable } from "@/components/ops/config-table";
import { DefinitionList } from "@/components/ops/definition-list";
import { HarnessTable } from "@/components/ops/harness-table";
import { LimitsGrid } from "@/components/ops/limits-grid";
import { Notice } from "@/components/ops/notice";
import { ConfigPanel, ConfigSection } from "@/components/ops/section";
import { SectionNav } from "@/components/ops/section-nav";
import { StatStrip } from "@/components/ops/stats";
import { VerificationProfiles } from "@/components/ops/verification-profiles";
import {
  asArray,
  asBoolean,
  asNumber,
  asObject,
  dateTimeOr,
  textOr,
} from "@/components/ops/data";
import { formatDuration, formatNumber, formatUsd } from "@/lib/format";
import { useApi } from "@/lib/hooks";
import { useWorkspace } from "@/lib/workspace";

const SECTIONS = [
  { id: "settings-infrastructure", label: "Infrastructure" },
  { id: "settings-repositories", label: "Repositories" },
  { id: "settings-scopes", label: "Scopes" },
  { id: "settings-harnesses", label: "Harnesses" },
  { id: "settings-models", label: "Models" },
  { id: "settings-egress", label: "Egress" },
  { id: "settings-workspaces", label: "Workspaces" },
  { id: "settings-budgets", label: "Budgets" },
  { id: "settings-verification", label: "Verification" },
  { id: "settings-review", label: "Review" },
  { id: "settings-hardening", label: "Hardening" },
  { id: "settings-intake", label: "Intake" },
  { id: "settings-sandbox", label: "Sandbox" },
  { id: "settings-limits", label: "Limits" },
];

function CodeList({ values = [], emptyLabel = "—" }) {
  const items = asArray(values);
  if (!items.length) return <span className="text-muted-foreground">{emptyLabel}</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {items.map((value, index) => (
        <Badge key={`${String(value)}:${index}`} variant="outline" size="xs" className="mono break-all">
          {String(value)}
        </Badge>
      ))}
    </div>
  );
}

function Muted({ children }) {
  return <span className="text-muted-foreground">{children}</span>;
}

function repoKind(repository) {
  const value = String(repository || "");
  if (value.startsWith("local:")) return "Local path";
  if (value.startsWith("file:")) return "File URL";
  return "Remote";
}

function budgetCost(value) {
  const number = asNumber(value);
  if (number === null) return <Muted>—</Muted>;
  if (number === 0) return <Muted>No limit</Muted>;
  return <span className="tabular">{formatUsd(number)}</span>;
}

function budgetTokens(value) {
  const number = asNumber(value);
  if (number === null) return <Muted>—</Muted>;
  if (number <= 0) return <Muted>No limit</Muted>;
  return <span className="tabular">{formatNumber(number)}</span>;
}

function budgetWallClock(value) {
  const number = asNumber(value);
  if (number === null) return <Muted>—</Muted>;
  if (number <= 0) return <Muted>No limit</Muted>;
  return <span className="tabular">{formatDuration(number)}</span>;
}

function SettingsSkeleton() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-36 rounded-panel" />
      <Skeleton className="h-24 rounded-panel" />
      <Skeleton className="h-20 rounded-panel" />
      <TableSkeleton rows={6} columns={5} />
      <div className="grid gap-4 lg:grid-cols-2">
        <Skeleton className="h-56 rounded-panel" />
        <Skeleton className="h-56 rounded-panel" />
      </div>
    </div>
  );
}

export function SettingsPage() {
  const { autoRefresh, refreshNonce } = useWorkspace();
  const api = useApi("/catalog", {
    pollMs: autoRefresh ? 30000 : 0,
    deps: [refreshNonce],
  });

  const response = asObject(api.data);
  const hasData = Boolean(api.data);

  const cube = asObject(response.cube);
  const temporal = asObject(response.temporal);
  const observability = asObject(response.observability);
  const storage = asObject(response.storage);
  const scopes = asArray(response.scopes);
  const harnesses = asArray(response.harnesses);
  const models = asArray(response.models);
  const egressPolicies = asArray(response.egress);
  const workspaces = asArray(response.workspaces);
  const budgets = asArray(response.budgets);
  const profiles = asArray(response.verification_profiles);
  const repositories = asArray(response.repositories).map((repository) => ({ repository }));
  const review = asObject(response.review);
  const hardening = asObject(response.hardening);
  const intake = asObject(response.intake);
  const sandbox = asObject(response.sandbox);
  const limits = asObject(response.limits);
  const warnings = asArray(response.warnings);
  const configPath = textOr(response.config_path, "");

  return (
    <div className="mx-auto w-full max-w-[1600px] px-4 py-5 sm:px-6 lg:px-8">
      <PageHeader
        eyebrow="Governance · Settings"
        title="Operator configuration"
        description="Read-only projection of the loaded factory configuration. Credentials and secret values are never returned by the console; mutating actions stay in the factory CLI."
        meta={
          <>
            <Badge variant="outline" size="sm" dot>
              <Lock aria-hidden="true" className="h-3 w-3" />
              Read-only
            </Badge>
            {response.environment ? (
              <Badge variant="muted" size="sm">
                {response.environment}
              </Badge>
            ) : null}
            <span>Schema v{textOr(response.schema_version, "—")}</span>
            <span aria-hidden="true" className="text-border-strong">
              ·
            </span>
            <span>Generated {dateTimeOr(response.generated_at)}</span>
          </>
        }
        actions={
          <Button
            variant="secondary"
            size="sm"
            icon={<RefreshCw />}
            onClick={api.refresh}
            loading={api.loading && hasData}
          >
            Refresh
          </Button>
        }
      />

      <div className="space-y-4">
        <Notice tone="info" icon={<Lock />} title="Read-only configuration">
          This console exposes configuration for inspection only. It cannot modify <span className="mono">factory.toml</span>,
          and credential values, model API keys and harness environment values are never returned — only names and
          presence flags. To change configuration, edit <span className="mono">factory.toml</span> and restart the
          factory. Review decisions remain in the factory CLI.
        </Notice>

        {api.error && hasData ? (
          <Notice
            tone="warning"
            title="Live refresh failed — showing the last successful snapshot"
            announce
            action={
              <Button variant="secondary" size="xs" onClick={api.refresh}>
                Retry
              </Button>
            }
          >
            {api.error.message || "The catalog endpoint could not be reached."}
          </Notice>
        ) : null}

        {api.error && !hasData ? (
          <Notice
            tone="danger"
            title="Configuration could not be loaded"
            announce
            action={
              <Button variant="secondary" size="xs" onClick={api.refresh}>
                Retry
              </Button>
            }
          >
            {api.error.message || "The catalog endpoint could not be reached."}
          </Notice>
        ) : null}

        {warnings.length ? (
          <Notice tone="warning" title={`${warnings.length} configuration warning${warnings.length === 1 ? "" : "s"}`}>
            <ul className="list-disc space-y-1 pl-4">
              {warnings.map((warning, index) => (
                <li key={index}>{warning}</li>
              ))}
            </ul>
          </Notice>
        ) : null}

        {!hasData && api.loading ? <SettingsSkeleton /> : null}

        {hasData ? (
          <>
            <Card>
              <CardHeader
                icon={<Info />}
                title="About this factory"
                description="Versions, schema and the configuration source for this console session."
              />
              <StatStrip
                columns={4}
                items={[
                  { label: "Environment", value: response.environment },
                  { label: "Factory version", value: response.factory_version },
                  { label: "Console version", value: response.console_version },
                  { label: "Config schema", value: response.schema_version, hint: "factory.toml schema version" },
                ]}
              />
              <CardBody className="border-t border-border">
                <DefinitionList
                  columns={2}
                  items={[
                    { label: "Config path", value: response.config_path, mono: true },
                    {
                      label: "Generated at",
                      value: dateTimeOr(response.generated_at),
                      hint: "Configuration is loaded at server start; restart the factory to reload it.",
                    },
                  ]}
                />
              </CardBody>
            </Card>

            <SectionNav items={SECTIONS} label="Configuration sections" />

            <ConfigSection
              id="settings-infrastructure"
              icon={<Server />}
              title="Infrastructure"
              description="Redacted endpoints for sandbox, workflow, telemetry and evidence storage. Credential values are never returned."
            >
              <div className="grid gap-4 lg:grid-cols-2">
                <ConfigPanel title="CubeSandbox">
                  <DefinitionList
                    columns={1}
                    items={[
                      { label: "API URL", value: cube.api_url, mono: true },
                      { label: "Template", value: cube.template_id, mono: true },
                      { label: "Proxy", value: cube.proxy, mono: true },
                      { label: "Version", value: cube.version },
                      {
                        label: "Credentials",
                        value: (
                          <BoolChip
                            value={asBoolean(cube.credentials_configured)}
                            onLabel="Configured"
                            offLabel="Not configured"
                            toneOn="success"
                            toneOff="muted"
                          />
                        ),
                        hint: "Presence only; the credential value never leaves the server.",
                      },
                    ]}
                  />
                </ConfigPanel>

                <ConfigPanel title="Temporal">
                  <DefinitionList
                    columns={1}
                    items={[
                      { label: "Host:port", value: temporal.host_port, mono: true },
                      { label: "Namespace", value: temporal.namespace, mono: true },
                      { label: "Task queue", value: temporal.task_queue, mono: true },
                      {
                        label: "TLS",
                        value: (
                          <BoolChip
                            value={asBoolean(temporal.tls)}
                            onLabel="Enabled"
                            offLabel="Disabled"
                            toneOn="success"
                            toneOff="warning"
                          />
                        ),
                      },
                      {
                        label: "API key env",
                        value: temporal.api_key_env,
                        mono: true,
                        hint: "Environment variable name only.",
                      },
                      {
                        label: "Payload keyring",
                        value: (
                          <BoolChip
                            value={asBoolean(temporal.payload_keyring_configured)}
                            onLabel="Configured"
                            offLabel="Not configured"
                            toneOn="success"
                            toneOff="muted"
                          />
                        ),
                      },
                    ]}
                  />
                </ConfigPanel>

                <ConfigPanel title="Observability">
                  <DefinitionList
                    columns={1}
                    items={[
                      {
                        label: "OTLP export",
                        value: (
                          <BoolChip
                            value={asBoolean(observability.otel_configured)}
                            onLabel="Configured"
                            offLabel="Not configured"
                            toneOn="success"
                            toneOff="warning"
                          />
                        ),
                      },
                      { label: "Service name", value: observability.service_name, mono: true },
                    ]}
                  />
                </ConfigPanel>

                <ConfigPanel title="Evidence storage">
                  <DefinitionList
                    columns={1}
                    items={[
                      { label: "Data directory", value: storage.data_dir, mono: true },
                      { label: "Runs on disk", value: formatNumber(storage.run_count, { fallback: "—" }) },
                      { label: "Changes on disk", value: formatNumber(storage.change_count, { fallback: "—" }) },
                    ]}
                  />
                </ConfigPanel>
              </div>
            </ConfigSection>

            <ConfigSection
              id="settings-repositories"
              icon={<FolderTree />}
              title="Repository allowlist"
              description="Repositories the factory will accept work for. Anything else is refused at run start."
              padded={false}
            >
              <ConfigTable
                rows={repositories}
                ariaLabel="Repository allowlist"
                columns={[
                  {
                    key: "repository",
                    label: "Repository",
                    render: (row) => <span className="mono break-all text-foreground">{row.repository}</span>,
                  },
                  {
                    key: "kind",
                    label: "Kind",
                    render: (row) => (
                      <Badge variant="outline" size="xs">
                        {repoKind(row.repository)}
                      </Badge>
                    ),
                  },
                ]}
                rowKey={(row) => row.repository}
                emptyIcon={<FolderTree />}
                emptyTitle="No repositories allowlisted"
                emptyDescription="The factory will refuse all work until at least one repository is configured."
                minWidth={560}
              />
            </ConfigSection>

            <ConfigSection
              id="settings-scopes"
              icon={<Layers />}
              title="Scopes"
              description="Tenancy boundaries. Unset fields inherit the global policy; a scope can narrow the policy but never widen it."
              padded={false}
            >
              <ConfigTable
                rows={scopes}
                ariaLabel="Scopes"
                columns={[
                  { key: "name", label: "Scope", render: (row) => <span className="mono font-medium text-foreground">{textOr(row.name)}</span> },
                  { key: "egress", label: "Egress", render: (row) => (row.egress ? <span className="mono">{row.egress}</span> : <Muted>Inherited</Muted>) },
                  { key: "model", label: "Model", render: (row) => (row.model ? <span className="mono">{row.model}</span> : <Muted>Inherited</Muted>) },
                  { key: "workspace", label: "Workspace", render: (row) => (row.workspace ? <span className="mono">{row.workspace}</span> : <Muted>Inherited</Muted>) },
                  { key: "budget", label: "Budget", render: (row) => (row.budget ? <span className="mono">{row.budget}</span> : <Muted>No budget</Muted>) },
                  {
                    key: "harnesses",
                    label: "Harnesses",
                    render: (row) => <CodeList values={row.harnesses} emptyLabel="All harnesses" />,
                  },
                  {
                    key: "review",
                    label: "Review",
                    render: (row) => (
                      <BoolChip
                        value={typeof row.review === "boolean" ? row.review : undefined}
                        onLabel="Review required"
                        offLabel="Review skipped"
                        unknownLabel="Inherit global"
                        toneOn="info"
                        toneOff="muted"
                      />
                    ),
                  },
                ]}
                rowKey={(row, index) => row?.name || index}
                emptyIcon={<Layers />}
                emptyTitle="No scopes configured"
                emptyDescription="A default scope is required before runs can start."
                minWidth={880}
              />
            </ConfigSection>

            <ConfigSection
              id="settings-harnesses"
              icon={<Layers />}
              title="Agent harnesses"
              description="Configured agent adapters. Environment variable values are never returned; only names are shown."
              padded={false}
            >
              <HarnessTable
                harnesses={harnesses}
                emptyDescription="factory.toml does not define any agent harnesses."
              />
            </ConfigSection>

            <ConfigSection
              id="settings-models"
              icon={<BrainCircuit />}
              title="Models"
              description="Model catalog entries. API keys are referenced by environment variable name only."
              padded={false}
            >
              <ConfigTable
                rows={models}
                ariaLabel="Model catalog"
                columns={[
                  { key: "name", label: "Name", render: (row) => <span className="mono font-medium text-foreground">{textOr(row.name)}</span> },
                  { key: "provider", label: "Provider", render: (row) => textOr(row.provider) },
                  { key: "model", label: "Model", mono: true },
                  { key: "base_url", label: "Base URL", mono: true },
                  { key: "api_key_env", label: "API key env", mono: true },
                  { key: "scopes", label: "Scopes", render: (row) => <CodeList values={row.scopes} emptyLabel="Any scope" /> },
                ]}
                rowKey={(row, index) => row?.name || index}
                emptyIcon={<BrainCircuit />}
                emptyTitle="No model catalog entries"
                emptyDescription="Model resolution is inherited from scope and harness configuration; no explicit model catalog is configured."
                minWidth={860}
              />
            </ConfigSection>

            <ConfigSection
              id="settings-egress"
              icon={<Network />}
              title="Egress policies"
              description="Named network policies the sandbox may be granted. Hosts are listed, never credentials."
              padded={false}
            >
              <ConfigTable
                rows={egressPolicies}
                ariaLabel="Egress policies"
                columns={[
                  { key: "name", label: "Policy", render: (row) => <span className="mono font-medium text-foreground">{textOr(row.name)}</span> },
                  {
                    key: "allow_internet",
                    label: "Internet",
                    render: (row) => (
                      <BoolChip
                        value={asBoolean(row.allow_internet)}
                        onLabel="Allowed"
                        offLabel="Blocked"
                        unknownLabel="Not set"
                        toneOn="warning"
                        toneOff="success"
                        size="xs"
                      />
                    ),
                  },
                  { key: "allow_out", label: "Allow out", render: (row) => <CodeList values={row.allow_out} emptyLabel="Nothing" /> },
                  { key: "deny_out", label: "Deny out", render: (row) => <CodeList values={row.deny_out} emptyLabel="Nothing" /> },
                  { key: "summary", label: "Summary", muted: true },
                ]}
                rowKey={(row, index) => row?.name || index}
                emptyIcon={<Network />}
                emptyTitle="No egress policies"
                emptyDescription="Sandboxes run without a named egress policy."
                minWidth={900}
              />
            </ConfigSection>

            <ConfigSection
              id="settings-workspaces"
              icon={<Container />}
              title="Workspaces"
              description="Sandbox workspace presets: templates, base packages and setup scripts."
              padded={false}
            >
              <ConfigTable
                rows={workspaces}
                ariaLabel="Workspaces"
                columns={[
                  { key: "name", label: "Workspace", render: (row) => <span className="mono font-medium text-foreground">{textOr(row.name)}</span> },
                  { key: "template", label: "Template", mono: true },
                  { key: "base_packages", label: "Base packages", render: (row) => <CodeList values={row.base_packages} emptyLabel="None" /> },
                  {
                    key: "snapshot_configured",
                    label: "Snapshot",
                    render: (row) => (
                      <BoolChip value={asBoolean(row.snapshot_configured)} onLabel="Yes" offLabel="No" size="xs" />
                    ),
                  },
                  {
                    key: "repository_cache",
                    label: "Repo cache",
                    render: (row) => (
                      <BoolChip value={asBoolean(row.repository_cache)} onLabel="Yes" offLabel="No" size="xs" />
                    ),
                  },
                  {
                    key: "setup_script_configured",
                    label: "Setup script",
                    render: (row) => (
                      <BoolChip value={asBoolean(row.setup_script_configured)} onLabel="Yes" offLabel="No" size="xs" />
                    ),
                  },
                ]}
                rowKey={(row, index) => row?.name || index}
                emptyIcon={<Container />}
                emptyTitle="No workspace presets"
                emptyDescription="Runs use the default sandbox template without extra packages or setup scripts."
                minWidth={840}
              />
            </ConfigSection>

            <ConfigSection
              id="settings-budgets"
              icon={<Gauge />}
              title="Budgets"
              description="Spend ceilings. A zero value disables that ceiling."
              padded={false}
            >
              <ConfigTable
                rows={budgets}
                ariaLabel="Budgets"
                columns={[
                  { key: "name", label: "Budget", render: (row) => <span className="mono font-medium text-foreground">{textOr(row.name)}</span> },
                  { key: "max_cost_usd", label: "Max cost", numeric: true, render: (row) => budgetCost(row.max_cost_usd) },
                  { key: "max_tokens", label: "Max tokens", numeric: true, render: (row) => budgetTokens(row.max_tokens) },
                  { key: "max_wall_clock_ms", label: "Max wall clock", numeric: true, render: (row) => budgetWallClock(row.max_wall_clock_ms) },
                ]}
                rowKey={(row, index) => row?.name || index}
                emptyIcon={<Gauge />}
                emptyTitle="No budgets configured"
                emptyDescription="Runs are not capped by a configured spend budget."
                minWidth={560}
              />
            </ConfigSection>

            <ConfigSection
              id="settings-verification"
              icon={<ListChecks />}
              title="Verification profiles"
              description="Deterministic gates executed inside the sandbox after the agent. Mandatory steps must pass."
            >
              <VerificationProfiles
                profiles={profiles}
                emptyDescription="factory.toml does not define a verification gate profile."
              />
            </ConfigSection>

            <ConfigSection
              id="settings-review"
              icon={<ShieldCheck />}
              title="Human review"
              description="Global review gate. Scope-level overrides can change whether individual runs pause."
            >
              {review.enabled === false ? (
                <Notice tone="warning" title="Review gate is disabled" className="mb-3">
                  Runs will not pause for human approval, and review commands are unavailable until it is enabled.
                </Notice>
              ) : null}
              <DefinitionList
                columns={3}
                items={[
                  {
                    label: "Enabled",
                    value: (
                      <BoolChip
                        value={asBoolean(review.enabled)}
                        onLabel="Enabled"
                        offLabel="Disabled"
                        toneOn="success"
                        toneOff="warning"
                      />
                    ),
                  },
                  { label: "Decision timeout", value: formatDuration(review.timeout_ms) },
                  {
                    label: "Sandbox while paused",
                    value: (
                      <BoolChip
                        value={asBoolean(review.suspend)}
                        onLabel="Checkpointed"
                        offLabel="Kept live"
                        toneOn="info"
                        toneOff="muted"
                      />
                    ),
                    hint: "Checkpointing is honoured only when the sandbox provider supports suspend.",
                  },
                  {
                    label: "Preview ports",
                    value: <CodeList values={asArray(review.preview_ports).map(String)} emptyLabel="None exposed" />,
                    span: "full",
                  },
                ]}
              />
            </ConfigSection>

            <ConfigSection
              id="settings-hardening"
              icon={<ShieldCheck />}
              title="Hardening"
              description="Threat-model controls applied to every sandbox. These are configured intentions, not measured evidence."
            >
              <DefinitionList
                columns={2}
                items={[
                  {
                    label: "Egress probing",
                    value: (
                      <BoolChip
                        value={asBoolean(hardening.egress_probe_enabled)}
                        onLabel="Enabled"
                        offLabel="Disabled"
                        toneOn="success"
                        toneOff="warning"
                      />
                    ),
                  },
                  {
                    label: "Behavior monitoring",
                    value: (
                      <BoolChip
                        value={asBoolean(hardening.behavior_monitor_enabled)}
                        onLabel="Enabled"
                        offLabel="Disabled"
                        toneOn="success"
                        toneOff="warning"
                      />
                    ),
                  },
                  {
                    label: "Trip severity",
                    value: hardening.trip_severity ? (
                      <StatusPill kind="severity" state={hardening.trip_severity} size="xs" />
                    ) : undefined,
                  },
                  {
                    label: "Require non-root",
                    value: (
                      <BoolChip
                        value={asBoolean(hardening.require_non_root)}
                        onLabel="Enforced"
                        offLabel="Not enforced"
                        toneOn="success"
                        toneOff="warning"
                      />
                    ),
                  },
                  {
                    label: "Open egress acknowledged",
                    value: (
                      <BoolChip
                        value={asBoolean(hardening.acknowledge_open_egress)}
                        onLabel="Acknowledged"
                        offLabel="Strict"
                        toneOn="warning"
                        toneOff="success"
                      />
                    ),
                  },
                  {
                    label: "Gate self-modification",
                    value: (
                      <BoolChip
                        value={asBoolean(hardening.allow_gate_self_modification)}
                        onLabel="Allowed"
                        offLabel="Blocked"
                        toneOn="danger"
                        toneOff="success"
                      />
                    ),
                  },
                  {
                    label: "Alert webhook",
                    value: (
                      <BoolChip
                        value={asBoolean(hardening.alert_webhook_configured)}
                        onLabel="Configured"
                        offLabel="Not configured"
                        toneOn="success"
                        toneOff="danger"
                      />
                    ),
                  },
                  { label: "Alert timeout", value: formatDuration(hardening.alert_timeout_ms) },
                  { label: "Egress canary URL", value: hardening.egress_canary_url, mono: true, span: "full" },
                  { label: "Metadata probe URL", value: hardening.metadata_probe_url, mono: true, span: "full" },
                ]}
              />
            </ConfigSection>

            <ConfigSection
              id="settings-intake"
              icon={<ScanSearch />}
              title="Task intake"
              description="Bounded intake advisory: readiness, task type and ambiguity signals for incoming work."
            >
              <DefinitionList
                columns={3}
                items={[
                  {
                    label: "Enabled",
                    value: (
                      <BoolChip
                        value={asBoolean(intake.enabled)}
                        onLabel="Enabled"
                        offLabel="Disabled"
                        toneOn="success"
                        toneOff="muted"
                      />
                    ),
                  },
                  { label: "Model", value: intake.model, mono: true },
                  { label: "Timeout", value: formatDuration(intake.timeout_ms) },
                ]}
              />
            </ConfigSection>

            <ConfigSection
              id="settings-sandbox"
              icon={<Container />}
              title="Sandbox"
              description="Sandbox defaults. Attach and preview are closed by default; opening them is audited per run."
            >
              <DefinitionList
                columns={2}
                items={[
                  {
                    label: "Allow attach",
                    value: (
                      <BoolChip
                        value={asBoolean(sandbox.allow_attach)}
                        onLabel="Allowed"
                        offLabel="Closed"
                        toneOn="warning"
                        toneOff="success"
                      />
                    ),
                  },
                  {
                    label: "Allow preview",
                    value: (
                      <BoolChip
                        value={asBoolean(sandbox.allow_preview)}
                        onLabel="Allowed"
                        offLabel="Closed"
                        toneOn="warning"
                        toneOff="success"
                      />
                    ),
                  },
                  {
                    label: "Setup script",
                    value: (
                      <BoolChip
                        value={asBoolean(sandbox.setup_script_configured)}
                        onLabel="Configured"
                        offLabel="Not configured"
                        toneOn="info"
                        toneOff="muted"
                      />
                    ),
                  },
                  { label: "Base packages", value: <CodeList values={sandbox.base_packages} emptyLabel="None" /> },
                  {
                    label: "Runtime egress allow list",
                    value: <CodeList values={sandbox.runtime_allow_out} emptyLabel="Empty" />,
                    span: "full",
                  },
                ]}
              />
            </ConfigSection>

            <ConfigSection
              id="settings-limits"
              icon={<Database />}
              title="Limits"
              description="Configured caps for command output, evidence, timeouts and sandbox resources."
            >
              <LimitsGrid limits={limits} />
            </ConfigSection>

            <Card>
              <CardBody className="flex flex-wrap items-center gap-x-4 gap-y-2 text-[11px] text-muted-foreground">
                <span className="inline-flex items-center gap-1.5">
                  <Lock aria-hidden="true" className="h-3 w-3" />
                  Read-only · values redacted server-side
                </span>
                {configPath ? (
                  <span>
                    Source <span className="mono break-all">{configPath}</span>
                  </span>
                ) : null}
                <span>Schema v{textOr(response.schema_version, "—")}</span>
                <span>Generated {dateTimeOr(response.generated_at)}</span>
                <span className={clsx("sm:ml-auto")}>
                  <a
                    href="#/operations"
                    className="font-medium text-foreground underline-offset-4 hover:text-primary hover:underline"
                  >
                    Open live operations
                  </a>
                </span>
              </CardBody>
            </Card>
          </>
        ) : null}
      </div>
    </div>
  );
}
