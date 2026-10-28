import { useEffect, useMemo, useState } from "react";
import clsx from "clsx";
import { ChevronRight, Download, FileDiff } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardFooter, CardHeader } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { Skeleton, SkeletonText } from "@/components/ui/skeleton";
import { formatBytes, formatNumber, parseUnifiedDiff } from "@/lib/format";

// The console patch endpoint returns a unified diff capped at 2 MiB. Line
// numbers are reconstructed from hunk headers so the viewer reads like the
// authoritative diff instead of a stylised approximation.
function annotateLines(lines) {
  let oldNumber = null;
  let newNumber = null;
  return lines.map((line) => {
    if (line.type === "hunk") {
      const match = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line.text);
      if (match) {
        oldNumber = Number(match[1]);
        newNumber = Number(match[2]);
      }
      return { type: "hunk", text: line.text };
    }
    if (line.type === "add") {
      const row = { type: "add", text: line.text, oldNo: null, newNo: newNumber };
      if (newNumber !== null) newNumber += 1;
      return row;
    }
    if (line.type === "del") {
      const row = { type: "del", text: line.text, oldNo: oldNumber, newNo: null };
      if (oldNumber !== null) oldNumber += 1;
      return row;
    }
    const row = { type: "context", text: line.text, oldNo: oldNumber, newNo: newNumber };
    if (oldNumber !== null) oldNumber += 1;
    if (newNumber !== null) newNumber += 1;
    return row;
  });
}

function DiffTable({ lines }) {
  const rows = useMemo(() => annotateLines(lines), [lines]);
  return (
    <div className="scroll-thin max-h-[460px] overflow-auto border-t border-border bg-background/40">
      <table className="w-full border-collapse font-mono text-[11px] leading-5">
        <tbody>
          {rows.map((row, index) => {
            if (row.type === "hunk") {
              return (
                <tr key={index} className="bg-info/10">
                  <td colSpan={4} className="whitespace-pre px-2 py-0.5 text-info">
                    {row.text}
                  </td>
                </tr>
              );
            }
            const rowClass = row.type === "add" ? "bg-success/10" : row.type === "del" ? "bg-danger/10" : "";
            const textClass =
              row.type === "add" ? "text-success" : row.type === "del" ? "text-danger" : "text-foreground/85";
            return (
              <tr key={index} className={rowClass}>
                <td className="w-10 select-none px-1.5 text-right text-[10px] text-muted-foreground/70">
                  {row.oldNo ?? ""}
                </td>
                <td className="w-10 select-none px-1.5 text-right text-[10px] text-muted-foreground/70">
                  {row.newNo ?? ""}
                </td>
                <td className="w-4 select-none text-center text-muted-foreground">
                  {row.type === "add" ? "+" : row.type === "del" ? "−" : " "}
                </td>
                <td className={clsx("whitespace-pre px-2", textClass)}>{row.text || " "}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// PatchCard shows the deliverable diff with per-file collapse, add/delete
// colouring and a Blob download. The text is fetched from /patch by the page so
// the card never depends on an unverified local patch summary.
export function PatchCard({ patchDetail, text, loading, error, onRetry, onDownload, downloading }) {
  const parsed = useMemo(() => parseUnifiedDiff(text || ""), [text]);
  const [expanded, setExpanded] = useState(() => new Set());

  useEffect(() => {
    setExpanded(parsed.files.length > 0 ? new Set([parsed.files[0].path]) : new Set());
  }, [parsed]);

  const toggle = (path) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  };

  const expandAll = () => setExpanded(new Set(parsed.files.map((file) => file.path)));
  const collapseAll = () => setExpanded(new Set());

  return (
    <Card>
      <CardHeader
        icon={<FileDiff />}
        title="Deliverable patch"
        description={patchDetail?.path ? `Unified diff recorded at ${patchDetail.path}.` : "Unified diff recorded for this run."}
        actions={
          <>
            {patchDetail?.available ? (
              <Badge variant="muted" size="xs">
                {formatNumber(parsed.files.length || patchDetail.files?.length || 0)} files
              </Badge>
            ) : null}
            {patchDetail?.available && onDownload ? (
              <Button variant="secondary" size="xs" icon={<Download />} loading={downloading} onClick={onDownload}>
                Download
              </Button>
            ) : null}
          </>
        }
      />
      <CardBody padded={false}>
        {!patchDetail?.available ? (
          <EmptyState
            compact
            icon={<FileDiff />}
            title="No patch recorded"
            description="No deliverable diff exists for this run. That is expected for runs that never produced a change, and reported honestly for the rest."
          />
        ) : loading ? (
          <div className="space-y-3 px-4 py-4 sm:px-5">
            <SkeletonText lines={2} />
            <Skeleton className="h-40 w-full" />
          </div>
        ) : error ? (
          <EmptyState
            compact
            icon={<FileDiff />}
            title="Patch could not be loaded"
            description={error.message || "The patch text could not be read from the console API."}
            action={
              onRetry ? (
                <Button variant="secondary" size="sm" onClick={onRetry}>
                  Retry
                </Button>
              ) : null
            }
          />
        ) : parsed.files.length === 0 ? (
          <EmptyState
            compact
            icon={<FileDiff />}
            title="Patch is empty"
            description="The stored patch contains no file diffs."
          />
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-border px-4 py-2 text-[11px] text-muted-foreground sm:px-5">
              <span>
                <span className="tabular text-foreground">{formatNumber(parsed.files.length)}</span> files
              </span>
              <span className="tabular font-medium text-success">+{formatNumber(parsed.additions)}</span>
              <span className="tabular font-medium text-danger">−{formatNumber(parsed.deletions)}</span>
              {patchDetail.bytes ? <span>{formatBytes(patchDetail.bytes)}</span> : null}
              {patchDetail.truncated ? (
                <Badge variant="warning" size="xs">
                  Patch truncated
                </Badge>
              ) : null}
              {parsed.binary || patchDetail.binary ? (
                <Badge variant="warning" size="xs">
                  Binary diff
                </Badge>
              ) : null}
              <div className="ml-auto flex items-center gap-1.5">
                <Button variant="ghost" size="xs" onClick={expandAll}>
                  Expand all
                </Button>
                <Button variant="ghost" size="xs" onClick={collapseAll}>
                  Collapse all
                </Button>
              </div>
            </div>
            <div className="space-y-2 px-4 py-3 sm:px-5">
              {parsed.files.map((file) => {
                const open = expanded.has(file.path);
                return (
                  <div key={file.path} className="overflow-hidden rounded-lg border border-border">
                    <button
                      type="button"
                      onClick={() => toggle(file.path)}
                      aria-expanded={open}
                      className="flex w-full items-center gap-2 bg-surface-raised px-3 py-2 text-left transition-colors hover:bg-muted/60"
                    >
                      <ChevronRight
                        aria-hidden="true"
                        className={clsx("h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")}
                      />
                      <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-foreground" title={file.path}>
                        {file.path}
                      </span>
                      <span className="tabular shrink-0 text-[10.5px] font-medium text-success">+{file.additions}</span>
                      <span className="tabular shrink-0 text-[10.5px] font-medium text-danger">−{file.deletions}</span>
                    </button>
                    {open ? <DiffTable lines={file.lines} /> : null}
                  </div>
                );
              })}
            </div>
          </>
        )}
      </CardBody>
      {patchDetail?.available && !loading && !error && parsed.files.length > 0 ? (
        <CardFooter>
          <span>Patch rendered from the same unified diff the CLI records; additions and deletions are parsed, not estimated.</span>
        </CardFooter>
      ) : null}
    </Card>
  );
}
