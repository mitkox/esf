import { useMemo, useState } from "react";
import clsx from "clsx";
import {
  ArrowRight,
  Check,
  ClipboardCheck,
  FileText,
  GitBranch,
  Play,
  Rocket,
  Server,
  ShieldCheck,
  Terminal,
} from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Card, CardHeader, CardBody } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { CopyButton } from "@/components/ui/copy-button";
import { Select } from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { EmptyState } from "@/components/ui/empty-state";
import { Skeleton } from "@/components/ui/skeleton";
import { useApi } from "@/lib/hooks";
import { runStateLabel, timeAgo, repoLabel } from "@/lib/format";

const STAGES = [
  {
    icon: FileText,
    title: "Intake advisory",
    description: "Optional typed readiness advice. It never changes the run result or the task text.",
  },
  {
    icon: ShieldCheck,
    title: "Policy and resource validation",
    description: "Repository allowlist, scope, harness, verification profile, budgets and egress are resolved once.",
  },
  {
    icon: Server,
    title: "Isolated provisioning",
    description: "One fresh CubeSandbox microVM, the resolved template, workspace preparation and an exact checkout.",
  },
  {
    icon: Play,
    title: "Agent execution",
    description: "The named harness runs one task with no host network path and no credential beyond its grant.",
  },
  {
    icon: Check,
    title: "Deterministic gates",
    description: "Repository-owned build and test commands decide the verification result, never the model.",
  },
  {
    icon: ClipboardCheck,
    title: "Human review gate",
    description: "When configured, the run pauses with evidence and previews until an operator decides.",
  },
  {
    icon: FileText,
    title: "Patch and evidence",
    description: "The deliverable patch, conditions, logs, security evidence and an auditable manifest are preserved.",
  },
  {
    icon: ShieldCheck,
    title: "Verified cleanup",
    description: "The sandbox is destroyed and the remaining inventory is checked before the run is final.",
  },
];

const REVISION_PATTERN = /^[0-9a-f]{40}$/;

function shellQuote(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}

function workflowCommand({ repository, revision, task, scope, harness, verification, model, review, wait, egress, workspace }) {
  const lines = ["factory run", `  --repo ${shellQuote(repository)}`, `  --rev ${shellQuote(revision)}`];
  if (task.trim()) lines.push(`  --task ${shellQuote(task.trim())}`);
  if (harness) lines.push(`  --agent ${shellQuote(harness)}`);
  if (verification) lines.push(`  --verification ${shellQuote(verification)}`);
  if (scope) lines.push(`  --scope ${shellQuote(scope)}`);
  if (model) lines.push(`  --model ${shellQuote(model)}`);
  if (egress) lines.push(`  --egress ${shellQuote(egress)}`);
  if (workspace) lines.push(`  --workspace ${shellQuote(workspace)}`);
  if (review) lines.push("  --review");
  if (!wait) lines.push("  --wait=false");
  return `${lines.join(" \\\n")}\n`;
}

function Field({ label, hint, error, children }) {
  return (
    <label className="block">
      <span className="mb-1.5 flex items-center justify-between gap-2 text-xs font-medium text-foreground">
        {label}
        {hint ? <span className="text-[11px] font-normal text-muted-foreground">{hint}</span> : null}
      </span>
      {children}
      {error ? <span className="mt-1.5 block text-[11px] text-danger">{error}</span> : null}
    </label>
  );
}

export function DispatchPage() {
  const catalog = useApi("/catalog", { pollMs: 60000 });
  const recent = useApi("/overview", { params: { window: "7d" }, pollMs: 60000 });
  const [repository, setRepository] = useState("");
  const [revision, setRevision] = useState("");
  const [task, setTask] = useState("");
  const [scope, setScope] = useState("");
  const [harness, setHarness] = useState("");
  const [verification, setVerification] = useState("");
  const [model, setModel] = useState("");
  const [egress, setEgress] = useState("");
  const [workspace, setWorkspace] = useState("");
  const [review, setReview] = useState(false);
  const [wait, setWait] = useState(true);
  const [touched, setTouched] = useState(false);

  const data = catalog.data;
  const allowed = data?.repositories || [];
  const scopes = data?.scopes || [];
  const harnesses = data?.harnesses || [];
  const profiles = data?.verification_profiles || [];
  const models = data?.models || [];
  const egressPolicies = data?.egress || [];
  const workspaces = data?.workspaces || [];
  const recentRuns = recent.data?.recent_runs || [];

  const defaults = useMemo(() => {
    const firstScope = scopes.find((entry) => entry.name === "default")?.name || scopes[0]?.name || "";
    const firstHarness = harnesses[0]?.name || "";
    const firstProfile = profiles[0]?.name || "";
    return { scope: firstScope, harness: firstHarness, verification: firstProfile };
  }, [scopes, harnesses, profiles]);

  const effectiveScope = scope || defaults.scope;
  const effectiveHarness = harness || defaults.harness;
  const effectiveProfile = verification || defaults.verification;

  const selectedScope = scopes.find((entry) => entry.name === effectiveScope) || null;
  const scopeRepositories = selectedScope?.repositories || [];
  const scopeHarnesses = selectedScope?.harnesses || [];
  const repositoryAllowed = repository
    ? (allowed.length === 0 || allowed.some((prefix) => repository.startsWith(prefix))) &&
      (scopeRepositories.length === 0 || scopeRepositories.some((prefix) => repository.startsWith(prefix)))
    : false;
  const harnessAllowed = !effectiveHarness
    ? false
    : scopeHarnesses.length === 0 || scopeHarnesses.includes(effectiveHarness);
  const errors = {
    repository: !repository
      ? "Repository is required."
      : !repositoryAllowed
        ? scopeRepositories.length > 0
          ? `Repository is not allowed in scope ${effectiveScope}.`
          : "Repository does not start with an allowed prefix."
        : "",
    revision: !revision ? "Exact revision is required." : REVISION_PATTERN.test(revision) ? "" : "Use the full 40-character commit SHA.",
    task: task.trim().length === 0 ? "Task text is required." : task.trim().length < 12 ? "Describe the change and its acceptance criteria." : "",
    harness: !effectiveHarness
      ? "No harness is configured."
      : !harnessAllowed
        ? `Harness is not allowed in scope ${effectiveScope}.`
        : "",
    verification: effectiveProfile ? "" : "No verification profile is configured.",
  };
  const valid = Object.values(errors).every((error) => !error);

  const command = workflowCommand({
    repository,
    revision,
    task,
    scope: effectiveScope,
    harness: effectiveHarness,
    verification: effectiveProfile,
    model,
    review,
    wait,
    egress,
    workspace,
  });

  function applyTemplate(run) {
    setRepository(run.repository || "");
    setRevision(run.baseline_sha || run.requested_revision || "");
    setScope(run.scope || defaults.scope);
    setHarness(run.agent_harness || defaults.harness);
    setVerification(run.verification_profile || defaults.verification);
    setTouched(true);
  }

  return (
    <div className="mx-auto w-full max-w-[1600px] px-4 py-5 sm:px-6 lg:px-8">
      <PageHeader
        eyebrow="Factory workflow"
        title="Dispatch a governed task"
        description="Compose the exact run request this factory will accept. The console generates the authenticated CLI command; it never submits work itself."
        actions={<Badge variant="outline">Read-only surface</Badge>}
      />

      <div className="mt-5 grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(360px,440px)]">
        <Card className="min-w-0">
          <CardHeader
            title="Run request"
            description="Task text and the exact revision are the only caller-controlled inputs. Every other value is operator policy."
            icon={<Rocket className="h-4 w-4 text-primary" />}
          />
          <CardBody className="space-y-4">
            <Field label="Task" hint={`${task.trim().length} characters`} error={touched ? errors.task : ""}>
              <textarea
                value={task}
                onChange={(event) => {
                  setTask(event.target.value);
                  setTouched(true);
                }}
                rows={5}
                placeholder={"Describe the change, its constraints and the acceptance criteria the deterministic gates should prove."}
                className={clsx(
                  "w-full resize-y rounded-lg border bg-background px-3 py-2 text-sm leading-6 outline-none transition",
                  "border-border placeholder:text-muted-foreground/60 focus:border-primary/60 focus:ring-4 focus:ring-primary/10",
                  touched && errors.task ? "border-danger/50" : "",
                )}
              />
            </Field>

            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Repository" hint={allowed.length ? `${allowed.length} allowlisted prefix${allowed.length === 1 ? "" : "es"}` : "No allowlist configured"} error={touched ? errors.repository : ""}>
                <Input
                  value={repository}
                  onChange={(event) => {
                    setRepository(event.target.value);
                    setTouched(true);
                  }}
                  placeholder="https://github.com/org/repo"
                  list="dispatch-repositories"
                  autoComplete="off"
                  spellCheck={false}
                />
                <datalist id="dispatch-repositories">
                  {allowed.map((prefix) => (
                    <option key={prefix} value={prefix} />
                  ))}
                </datalist>
              </Field>
              <Field label="Revision" hint="full commit SHA" error={touched ? errors.revision : ""}>
                <Input
                  value={revision}
                  onChange={(event) => {
                    setRevision(event.target.value.trim().toLowerCase());
                    setTouched(true);
                  }}
                  placeholder="40-character SHA"
                  autoComplete="off"
                  spellCheck={false}
                  className="font-mono"
                />
              </Field>
            </div>

            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Scope" hint={scopes.length ? "" : "not configured"}>
                <Select
                  ariaLabel="Scope"
                  value={effectiveScope}
                  onChange={(event) => setScope(event.target.value)}
                  options={scopes.map((entry) => ({ value: entry.name, label: entry.name }))}
                />
              </Field>
              <Field label="Agent harness" error={touched ? errors.harness : ""}>
                <Select
                  ariaLabel="Agent harness"
                  value={effectiveHarness}
                  onChange={(event) => setHarness(event.target.value)}
                  options={harnesses.map((entry) => ({
                    value: entry.name,
                    label: `${entry.name}${entry.type ? ` · ${entry.type}` : ""}`,
                  }))}
                />
              </Field>
              <Field label="Verification profile" error={touched ? errors.verification : ""}>
                <Select
                  ariaLabel="Verification profile"
                  value={effectiveProfile}
                  onChange={(event) => setVerification(event.target.value)}
                  options={profiles.map((entry) => ({
                    value: entry.name,
                    label: `${entry.name} · ${entry.steps.length} gate${entry.steps.length === 1 ? "" : "s"}`,
                  }))}
                />
              </Field>
              <Field label="Model override" hint="optional">
                <Select
                  ariaLabel="Model override"
                  value={model}
                  onChange={(event) => setModel(event.target.value)}
                  options={[{ value: "", label: "Harness default" }, ...models.map((entry) => ({ value: entry.name, label: `${entry.name} · ${entry.model || entry.provider}` }))]}
                />
              </Field>
              <Field label="Egress policy" hint="optional">
                <Select
                  ariaLabel="Egress policy"
                  value={egress}
                  onChange={(event) => setEgress(event.target.value)}
                  options={[{ value: "", label: "Scope default" }, ...egressPolicies.map((entry) => ({ value: entry.name, label: entry.name }))]}
                />
              </Field>
              <Field label="Workspace" hint="optional">
                <Select
                  ariaLabel="Workspace"
                  value={workspace}
                  onChange={(event) => setWorkspace(event.target.value)}
                  options={[{ value: "", label: "Scope default" }, ...workspaces.map((entry) => ({ value: entry.name, label: entry.name }))]}
                />
              </Field>
            </div>

            <div className="flex flex-wrap items-center gap-x-6 gap-y-3 rounded-lg border border-border bg-surface-raised/60 px-3 py-2.5">
              <label className="flex items-center gap-2 text-xs">
                <input type="checkbox" className="h-3.5 w-3.5 accent-[var(--primary)]" checked={review} onChange={(event) => setReview(event.target.checked)} />
                Pause for human review (<span className="font-mono text-[11px]">--review</span>)
              </label>
              <label className="flex items-center gap-2 text-xs">
                <input type="checkbox" className="h-3.5 w-3.5 accent-[var(--primary)]" checked={wait} onChange={(event) => setWait(event.target.checked)} />
                Wait for completion in the terminal
              </label>
            </div>

            {catalog.error ? <p className="rounded-lg border border-danger/30 bg-danger/5 px-3 py-2 text-xs text-danger">The operator catalog could not be loaded: {catalog.error.message}</p> : null}
            {data?.warnings?.length ? (
              <div className="rounded-lg border border-warning/30 bg-warning/5 px-3 py-2 text-xs text-warning">
                <p className="font-medium">Configuration warnings</p>
                <ul className="mt-1 list-disc space-y-0.5 pl-4">
                  {data.warnings.slice(0, 3).map((warning, index) => (
                    <li key={index}>{warning}</li>
                  ))}
                </ul>
              </div>
            ) : null}
          </CardBody>
        </Card>

        <div className="space-y-4">
          <Card>
            <CardHeader title="Generated command" description="Run this on the factory host as an authenticated operator." icon={<Terminal className="h-4 w-4 text-primary" />} />
            <CardBody className="space-y-3">
              <div className="relative">
                <pre className="scroll-thin max-h-72 overflow-auto rounded-lg border border-border bg-background p-3 font-mono text-[11.5px] leading-5">
                  <code>{valid ? command : "# Complete the required fields to generate the command."}</code>
                </pre>
                <div className="absolute right-2 top-2">
                  <CopyButton value={valid ? command : ""} label="Copy" />
                </div>
              </div>
              <p className="text-[11px] leading-5 text-muted-foreground">
                Nothing is submitted by this page. The console is a read-only evidence and command surface; the CLI remains the audited
                action boundary.
              </p>
              {!valid && touched ? (
                <ul className="space-y-1 text-[11px] text-danger">
                  {Object.entries(errors)
                    .filter(([, error]) => error)
                    .map(([field, error]) => (
                      <li key={field}>· {error}</li>
                    ))}
                </ul>
              ) : null}
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="Recent run configuration" description="Reuse a repository, revision and harness that already ran." icon={<GitBranch className="h-4 w-4 text-primary" />} />
            <CardBody padded={false}>
              {recent.loading ? (
                <div className="space-y-2 p-4">
                  <Skeleton className="h-9 w-full" />
                  <Skeleton className="h-9 w-full" />
                </div>
              ) : recentRuns.length === 0 ? (
                <EmptyState compact icon={<GitBranch className="h-4 w-4" />} title="No recent runs" description="Run evidence will appear here once the factory has executed a task." />
              ) : (
                <ul className="divide-y divide-border">
                  {recentRuns.slice(0, 5).map((run) => (
                    <li key={run.run_id} className="flex items-center gap-3 px-4 py-2.5">
                      <div className="min-w-0 flex-1">
                        <p className="truncate font-mono text-[11px] text-foreground">{repoLabel(run.repository)}</p>
                        <p className="truncate text-[11px] text-muted-foreground">
                          {run.agent_harness} · {run.verification_profile} · {run.factory_result} · {timeAgo(run.completed_at || run.started_at)}
                        </p>
                      </div>
                      <Button size="sm" variant="ghost" onClick={() => applyTemplate(run)} aria-label={`Use configuration from ${run.run_id}`}>
                        Use
                        <ArrowRight className="h-3 w-3" />
                      </Button>
                    </li>
                  ))}
                </ul>
              )}
            </CardBody>
          </Card>
        </div>
      </div>

      <Card className="mt-4">
        <CardHeader title="What happens to a dispatched task" description="The factory workflow, in order. Each stage records evidence that survives the sandbox." icon={<ClipboardCheck className="h-4 w-4 text-primary" />} />
        <CardBody>
          <ol className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            {STAGES.map((stage, index) => {
              const Icon = stage.icon;
              return (
                <li key={stage.title} className="rounded-lg border border-border bg-surface-raised/50 p-3">
                  <div className="flex items-center gap-2">
                    <span className="grid h-6 w-6 shrink-0 place-items-center rounded-md border border-border bg-surface text-[10px] font-semibold text-muted-foreground">
                      {String(index + 1).padStart(2, "0")}
                    </span>
                    <Icon className="h-3.5 w-3.5 text-primary" />
                    <p className="text-xs font-medium">{stage.title}</p>
                  </div>
                  <p className="mt-1.5 text-[11px] leading-5 text-muted-foreground">{stage.description}</p>
                </li>
              );
            })}
          </ol>
        </CardBody>
      </Card>

      <p className="mt-4 text-[11px] text-muted-foreground">
        Current factory version {data?.factory_version || "—"} · {harnesses.length} harnesses · {profiles.length} verification profiles ·{" "}
        {scopes.length} scopes · {allowed.length} approved repository prefixes. Decisions and cancellation stay in the CLI; see the
        operator guide.
      </p>
    </div>
  );
}
