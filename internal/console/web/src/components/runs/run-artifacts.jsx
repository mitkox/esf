import { useMemo, useState } from "react";
import clsx from "clsx";
import { Eye, FolderOpen } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { SearchInput } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Table, THead, TH, TR, TD, TableEmpty } from "@/components/ui/table";
import { formatBytes, formatNumber } from "@/lib/format";
import { artifactKindLabel, artifactKindVariant } from "./utils";

const KINDS = ["text", "json", "log", "patch", "other"];

// EvidenceBrowserCard lists every artifact collected for the run and previews
// the allowlisted text ones through the bounded artifact endpoint. Binary and
// non-allowlisted files are shown honestly instead of being silently omitted.
export function EvidenceBrowserCard({ artifacts = [], onPreview }) {
  const [search, setSearch] = useState("");
  const [kind, setKind] = useState("");

  const presentKinds = useMemo(() => {
    const values = new Set(artifacts.map((artifact) => artifact.kind).filter(Boolean));
    return KINDS.filter((value) => values.has(value));
  }, [artifacts]);

  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return artifacts.filter((artifact) => {
      if (needle && !String(artifact.path || "").toLowerCase().includes(needle)) return false;
      if (kind && artifact.kind !== kind) return false;
      return true;
    });
  }, [artifacts, search, kind]);

  const previewable = artifacts.filter((artifact) => artifact.previewable).length;

  return (
    <Card>
      <CardHeader
        icon={<FolderOpen />}
        title="Evidence browser"
        description="Bounded evidence files collected from the run directory. Previewable files are read through the console artifact endpoint."
        actions={
          <Badge variant="muted" size="xs">
            {formatNumber(artifacts.length)} file{artifacts.length === 1 ? "" : "s"} · {formatNumber(previewable)} previewable
          </Badge>
        }
      />
      <CardBody padded={false}>
        <div className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-2 sm:px-5">
          <SearchInput
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Filter evidence paths…"
            aria-label="Filter evidence paths"
            className="w-full sm:w-64"
          />
          <Select
            ariaLabel="Filter evidence by kind"
            value={kind}
            onChange={(event) => setKind(event.target.value)}
            options={[{ value: "", label: "All kinds" }, ...presentKinds.map((value) => ({ value, label: artifactKindLabel(value) }))]}
            className="w-36"
          />
          <span className="tabular ml-auto text-[11px] text-muted-foreground">
            {formatNumber(filtered.length)} of {formatNumber(artifacts.length)}
          </span>
        </div>

        <Table minWidth={560} className="max-h-[420px] overflow-y-auto">
          <THead>
            <TR className="hover:bg-transparent">
              <TH>Evidence</TH>
              <TH>Kind</TH>
              <TH numeric>Size</TH>
              <TH className="w-24 text-right">Preview</TH>
            </TR>
          </THead>
          <tbody>
            {filtered.length === 0 ? (
              <TableEmpty
                colSpan={4}
                icon={<FolderOpen />}
                title={artifacts.length === 0 ? "No evidence files" : "No evidence matches this filter"}
                description={
                  artifacts.length === 0
                    ? "No artifacts were collected in this run directory."
                    : "Clear the path or kind filter to see the rest of the evidence."
                }
              />
            ) : (
              filtered.map((artifact) => (
                <TR key={artifact.path} className={clsx(artifact.previewable && "hover:bg-surface-raised/60")}>
                  <TD className="max-w-0">
                    <button
                      type="button"
                      disabled={!artifact.previewable}
                      onClick={() => onPreview?.(artifact.path, artifact.path)}
                      title={artifact.path}
                      className={clsx(
                        "block min-h-[24px] w-full truncate py-0.5 text-left font-mono text-[11.5px] leading-5",
                        artifact.previewable
                          ? "text-foreground hover:text-primary"
                          : "cursor-default text-muted-foreground",
                      )}
                    >
                      {artifact.path}
                    </button>
                    {artifact.error ? (
                      <span className="mt-0.5 block truncate text-[10px] text-warning" title={artifact.error}>
                        {artifact.error}
                      </span>
                    ) : null}
                  </TD>
                  <TD>
                    <Badge variant={artifactKindVariant(artifact.kind)} size="xs">
                      {artifactKindLabel(artifact.kind)}
                    </Badge>
                  </TD>
                  <TD numeric className="whitespace-nowrap">
                    {formatBytes(artifact.bytes)}
                  </TD>
                  <TD align="right">
                    {artifact.previewable ? (
                      <Button variant="ghost" size="xs" icon={<Eye />} onClick={() => onPreview?.(artifact.path, artifact.path)}>
                        Preview
                      </Button>
                    ) : (
                      <Badge
                        variant="muted"
                        size="xs"
                        title="Binary content or a path outside the console preview allowlist."
                      >
                        Not previewable
                      </Badge>
                    )}
                  </TD>
                </TR>
              ))
            )}
          </tbody>
        </Table>
      </CardBody>
      <CardFooter>
        <span>
          Only allowlisted text evidence is previewable; reads are bounded by the console. Empty or binary files are
          reported rather than rendered.
        </span>
      </CardFooter>
    </Card>
  );
}
