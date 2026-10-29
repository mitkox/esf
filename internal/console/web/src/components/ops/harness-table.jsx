import { Badge } from "@/components/ui/badge";
import { formatDuration } from "@/lib/format";
import { TD, TH, THead, TR, Table, TableEmpty } from "@/components/ui/table";
import { BoolChip } from "./chips";
import { asArray, asNumber, textOr } from "./data";

function PinChips({ harness }) {
  return (
    <div className="flex flex-col items-start gap-1">
      <BoolChip
        value={typeof harness?.binary_pinned === "boolean" ? harness.binary_pinned : undefined}
        onLabel="binary pinned"
        offLabel="binary floating"
        toneOn="success"
        toneOff="warning"
        size="xs"
      />
      <BoolChip
        value={typeof harness?.runtime_pinned === "boolean" ? harness.runtime_pinned : undefined}
        onLabel="runtime pinned"
        offLabel="runtime floating"
        toneOn="success"
        toneOff="muted"
        size="xs"
      />
    </div>
  );
}

function CacheChips({ harness }) {
  return (
    <div className="flex flex-col items-start gap-1">
      <BoolChip
        value={typeof harness?.catalog_cache_configured === "boolean" ? harness.catalog_cache_configured : undefined}
        onLabel="catalog cache"
        offLabel="no catalog cache"
        toneOn="info"
        toneOff="muted"
        size="xs"
      />
      <BoolChip
        value={typeof harness?.provider_files_configured === "boolean" ? harness.provider_files_configured : undefined}
        onLabel="provider files"
        offLabel="no provider files"
        toneOn="info"
        toneOff="muted"
        size="xs"
      />
    </div>
  );
}

// HarnessTable is shared by Operations (inventory) and Settings (catalog) so
// the same harness never renders two different ways. Environment variable
// values are never requested; only names arrive from the API.
export function HarnessTable({ harnesses = [], className, emptyDescription }) {
  const rows = asArray(harnesses);
  return (
    <Table className={className} minWidth={1280} aria-label="Agent harness inventory">
      <THead>
        <TR>
          <TH>Harness</TH>
          <TH>Type</TH>
          <TH>Model</TH>
          <TH>Provider</TH>
          <TH numeric>Timeout</TH>
          <TH>Preinstalled</TH>
          <TH>Pinning</TH>
          <TH>Packages</TH>
          <TH>Cache / provider files</TH>
          <TH>Pass env</TH>
          <TH>Prompt mode</TH>
        </TR>
      </THead>
      <tbody>
        {rows.length ? (
          rows.map((harness, index) => {
            const timeoutSeconds = asNumber(harness?.timeout_seconds);
            const passEnv = asArray(harness?.pass_env_names);
            return (
              <TR key={harness?.name || index}>
                <TD>
                  <div className="font-medium text-foreground">{textOr(harness?.name)}</div>
                  {harness?.binary ? (
                    <div className="mono max-w-[260px] truncate text-muted-foreground" title={harness.binary}>
                      {harness.binary}
                    </div>
                  ) : (
                    <div className="text-[11px] text-muted-foreground">No binary path reported</div>
                  )}
                </TD>
                <TD>
                  {harness?.type ? (
                    <Badge variant="outline" size="xs">
                      {harness.type}
                    </Badge>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TD>
                <TD>
                  <div className="flex flex-wrap items-center gap-1.5">
                    {harness?.model ? <span className="mono">{harness.model}</span> : <span className="text-muted-foreground">—</span>}
                    {harness?.thinking_level ? (
                      <Badge variant="muted" size="xs" title="Thinking level">
                        thinking {harness.thinking_level}
                      </Badge>
                    ) : null}
                  </div>
                </TD>
                <TD muted>{textOr(harness?.provider)}</TD>
                <TD numeric>{timeoutSeconds === null ? "—" : formatDuration(timeoutSeconds * 1000)}</TD>
                <TD>
                  <BoolChip
                    value={typeof harness?.preinstalled === "boolean" ? harness.preinstalled : undefined}
                    onLabel="Yes"
                    offLabel="No"
                    toneOn="success"
                    toneOff="muted"
                    size="xs"
                  />
                </TD>
                <TD>
                  <PinChips harness={harness} />
                </TD>
                <TD>
                  {asArray(harness?.packages).length ? (
                    <div className="flex max-w-[200px] flex-col gap-0.5">
                      {asArray(harness.packages).map((name, packageIndex) => (
                        <span key={`${name}:${packageIndex}`} className="mono text-muted-foreground">
                          {name}
                        </span>
                      ))}
                    </div>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TD>
                <TD>
                  <CacheChips harness={harness} />
                </TD>
                <TD>
                  {passEnv.length ? (
                    <div className="flex max-w-[180px] flex-col gap-0.5">
                      {passEnv.map((name) => (
                        <span key={name} className="mono text-muted-foreground" title="Environment variable name (value never returned)">
                          {name}
                        </span>
                      ))}
                    </div>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TD>
                <TD muted>{textOr(harness?.prompt_mode, "default")}</TD>
              </TR>
            );
          })
        ) : (
          <TableEmpty
            colSpan={11}
            title="No harnesses configured"
            description={emptyDescription || "factory.toml does not define any agent harnesses."}
          />
        )}
      </tbody>
    </Table>
  );
}
