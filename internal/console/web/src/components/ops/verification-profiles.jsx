import clsx from "clsx";
import { ListChecks } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { formatDuration } from "@/lib/format";
import { TD, TH, THead, TR, Table, TableEmpty } from "@/components/ui/table";
import { BoolChip } from "./chips";
import { asArray, textOr } from "./data";

// VerificationProfiles renders the deterministic gate catalog. Each profile is
// a bordered panel (not a nested card) so it works both inside a page section
// and inside a settings card body.
export function VerificationProfiles({ profiles = [], className, emptyDescription }) {
  const items = asArray(profiles);
  if (!items.length) {
    return (
      <EmptyState
        compact
        icon={<ListChecks />}
        title="No verification profiles"
        description={emptyDescription || "factory.toml does not define a verification gate profile."}
      />
    );
  }
  return (
    <div className={clsx("grid gap-4 2xl:grid-cols-2", className)}>
      {items.map((profile, index) => {
        const steps = asArray(profile?.steps);
        return (
          <div key={profile?.name || index} className="min-w-0 overflow-hidden rounded-lg border border-border">
            <div className="flex flex-wrap items-center gap-2 border-b border-border bg-surface-raised px-3 py-2">
              <span className="mono text-foreground">{textOr(profile?.name, "Unnamed profile")}</span>
              <Badge variant="outline" size="xs">
                {steps.length} step{steps.length === 1 ? "" : "s"}
              </Badge>
              <span className="ml-auto text-[11px] text-muted-foreground">Runs before a change is accepted</span>
            </div>
            <Table minWidth={640} aria-label={`Verification profile ${textOr(profile?.name, "")} steps`}>
              <THead>
                <TR>
                  <TH>Step</TH>
                  <TH>Command</TH>
                  <TH>Required</TH>
                  <TH numeric>Timeout</TH>
                </TR>
              </THead>
              <tbody>
                {steps.length ? (
                  steps.map((step, stepIndex) => (
                    <TR key={step?.id || stepIndex}>
                      <TD>
                        <div className="mono text-foreground">{textOr(step?.id, "step")}</div>
                        {step?.description ? (
                          <p className="mt-0.5 max-w-[240px] text-[11px] leading-snug text-muted-foreground">
                            {step.description}
                          </p>
                        ) : null}
                        {step?.dir ? (
                          <p className="mono mt-0.5 text-[11px] text-muted-foreground" title="Working directory">
                            cwd {step.dir}
                          </p>
                        ) : null}
                      </TD>
                      <TD>
                        <span className="mono block max-w-[420px] break-words">
                          {asArray(step?.argv).length ? step.argv.join(" ") : "—"}
                        </span>
                      </TD>
                      <TD>
                        <BoolChip
                          value={typeof step?.mandatory === "boolean" ? step.mandatory : undefined}
                          onLabel="Required"
                          offLabel="Advisory"
                          toneOn="info"
                          toneOff="muted"
                          size="xs"
                        />
                      </TD>
                      <TD numeric>{formatDuration(step?.timeout_ms)}</TD>
                    </TR>
                  ))
                ) : (
                  <TableEmpty colSpan={4} title="No steps" description="This profile has no verification steps." />
                )}
              </tbody>
            </Table>
          </div>
        );
      })}
    </div>
  );
}
