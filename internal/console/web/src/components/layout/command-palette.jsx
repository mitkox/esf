import { useEffect, useId, useRef, useState } from "react";
import clsx from "clsx";
import { ArrowDown, ArrowUp, CornerDownLeft, GitBranch, Play, Search, X } from "lucide-react";
import { NAV_GROUPS, hrefFor } from "@/routes";
import { useApi, useDebounced } from "@/lib/hooks";
import { repoLabel, timeAgo, truncate } from "@/lib/format";
import { StatusPill } from "@/components/ui/status-pill";
import { navIcon } from "./nav-icons";
import { normalizeHref } from "./links";

const STATIC_ENTRIES = NAV_GROUPS.flatMap((group) =>
  group.items.map((item) => ({
    id: `nav:${item.id}`,
    kind: "nav",
    title: item.label,
    subtitle: group.label,
    href: hrefFor(item.id),
    Icon: navIcon(item.icon),
    mono: false,
    state: "",
  })),
);

function entryFromRun(raw) {
  if (!raw || !raw.run_id) return null;
  return {
    kind: "run",
    id: raw.run_id,
    title: raw.run_id,
    subtitle: [
      raw.repository ? repoLabel(raw.repository) : "",
      raw.agent_harness || "",
      raw.started_at ? timeAgo(raw.started_at) : "",
    ]
      .filter(Boolean)
      .join(" · "),
    status: raw.factory_result || raw.state || raw.agent_result || "",
    href: raw.href,
  };
}

function entryFromChange(raw) {
  if (!raw || !raw.change_id) return null;
  return {
    kind: "change",
    id: raw.change_id,
    title: raw.change_id,
    subtitle: [
      raw.repository ? repoLabel(raw.repository) : "",
      raw.task_preview ? truncate(raw.task_preview, 64) : "",
    ]
      .filter(Boolean)
      .join(" · "),
    status: raw.status || raw.last_result || "",
    href: raw.href,
  };
}

// GET /search -> {query, results:[{kind,id,title,subtitle,status,href}],
// duration_ms, index}. Alternate shapes (runs[]/changes[]) are tolerated so the
// palette keeps working while endpoints evolve.
function collectResults(data) {
  const entries = [];
  if (Array.isArray(data?.results)) entries.push(...data.results);
  if (Array.isArray(data?.runs)) entries.push(...data.runs.map(entryFromRun));
  if (Array.isArray(data?.changes)) entries.push(...data.changes.map(entryFromChange));

  const seen = new Set();
  const items = [];
  for (const raw of entries) {
    if (!raw || typeof raw !== "object") continue;
    const kind = String(raw.kind || "").toLowerCase();
    const id = String(raw.id || raw.run_id || raw.change_id || "");
    if (!id || (kind !== "run" && kind !== "change")) continue;
    const key = `${kind}:${id}`;
    if (seen.has(key)) continue;
    seen.add(key);
    const title = raw.title || id;
    items.push({
      id: key,
      kind,
      title,
      mono: String(title) === String(id),
      subtitle: raw.subtitle || raw.description || "",
      state: raw.status || raw.state || "",
      href: normalizeHref(raw.href, hrefFor(kind, id)),
      Icon: kind === "run" ? Play : GitBranch,
    });
  }
  return {
    runs: items.filter((item) => item.kind === "run"),
    changes: items.filter((item) => item.kind === "change"),
  };
}

export function CommandPalette({ open, onClose }) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const debouncedQuery = useDebounced(query.trim(), 180);
  const listboxID = useId();
  const inputRef = useRef(null);
  const listRef = useRef(null);

  const search = useApi("/search", {
    params: { q: debouncedQuery, limit: 8 },
    enabled: open && debouncedQuery.length > 0,
  });

  const lowered = debouncedQuery.toLowerCase();
  const navMatches = lowered
    ? STATIC_ENTRIES.filter(
        (entry) =>
          entry.title.toLowerCase().includes(lowered) || entry.subtitle.toLowerCase().includes(lowered),
      )
    : STATIC_ENTRIES;
  const results = collectResults(search.data);

  const groups = [];
  if (navMatches.length) groups.push({ label: "Navigate", items: navMatches });
  if (debouncedQuery) {
    if (results.runs.length) groups.push({ label: "Runs", items: results.runs });
    if (results.changes.length) groups.push({ label: "Changes", items: results.changes });
  }
  let runningIndex = 0;
  const indexedGroups = groups.map((group) => ({
    ...group,
    items: group.items.map((item) => ({ ...item, index: runningIndex++ })),
  }));
  const items = indexedGroups.flatMap((group) => group.items);
  const searching = debouncedQuery.length > 0 && search.loading && !search.data;

  useEffect(() => {
    if (!open) return undefined;
    setQuery("");
    setActive(0);
    const focus = window.requestAnimationFrame(() => inputRef.current?.focus());
    const previous = document.activeElement;
    return () => {
      window.cancelAnimationFrame(focus);
      if (previous instanceof HTMLElement) previous.focus();
    };
  }, [open]);

  useEffect(() => {
    setActive(0);
  }, [debouncedQuery]);

  useEffect(() => {
    if (active >= items.length && items.length > 0) setActive(0);
  }, [active, items.length]);

  useEffect(() => {
    if (!open) return;
    const node = listRef.current?.querySelector('[data-selected="true"]');
    node?.scrollIntoView({ block: "nearest" });
  }, [active, open]);

  function activate(item) {
    if (!item) return;
    onClose?.();
    const next = normalizeHref(item.href, "#/overview");
    if (window.location.hash !== next) window.location.hash = next;
  }

  function handleKeyDown(event) {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (!items.length) return;
      const direction = event.key === "ArrowDown" ? 1 : -1;
      setActive((current) => (current + direction + items.length) % items.length);
      return;
    }
    if (event.key === "Home" || event.key === "End") {
      event.preventDefault();
      if (!items.length) return;
      setActive(event.key === "Home" ? 0 : items.length - 1);
      return;
    }
    if (event.key === "Enter") {
      event.preventDefault();
      activate(items[active]);
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      onClose?.();
    }
  }

  if (!open) return null;

  const hasResults = items.length > 0;
  const showEmpty = debouncedQuery.length > 0 && !search.loading && !hasResults;

  return (
    <div className="fixed inset-0 z-[70] flex items-start justify-center px-3 pt-[10vh] sm:pt-[14vh]">
      <div
        aria-hidden="true"
        onClick={onClose}
        className="dialog-overlay absolute inset-0 bg-black/40 backdrop-blur-[2px] dark:bg-black/60"
      />
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        className="dialog-panel relative flex w-full max-w-xl flex-col overflow-hidden rounded-panel border border-border-strong bg-surface-overlay shadow-popover"
      >
        <div className="flex items-center gap-2.5 border-b border-border px-3 py-2.5">
          <Search aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />
          <input
            ref={inputRef}
            type="text"
            role="combobox"
            aria-expanded="true"
            aria-controls={listboxID}
            aria-activedescendant={hasResults ? `${listboxID}-option-${active}` : undefined}
            aria-autocomplete="list"
            aria-label="Search runs, changes and pages"
            autoComplete="off"
            spellCheck={false}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Search runs, changes and pages…"
            className="h-7 min-w-0 flex-1 border-0 bg-transparent p-0 text-sm text-foreground outline-none placeholder:text-muted-foreground/60 focus-visible:outline-none"
          />
          {search.loading && debouncedQuery ? (
            <span className="h-3.5 w-3.5 shrink-0 animate-spin rounded-full border-2 border-muted-foreground/30 border-t-muted-foreground" />
          ) : null}
          {query ? (
            <button
              type="button"
              onClick={() => {
                setQuery("");
                inputRef.current?.focus();
              }}
              aria-label="Clear search"
              className="grid h-6 w-6 shrink-0 place-items-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            >
              <X aria-hidden="true" className="h-3 w-3" />
            </button>
          ) : (
            <span className="kbd hidden shrink-0 sm:inline-flex" aria-hidden="true">
              esc
            </span>
          )}
        </div>

        <div ref={listRef} role="listbox" id={listboxID} aria-label="Command results" className="scroll-thin max-h-[min(56vh,26rem)] overflow-y-auto p-1">
          {searching ? (
            <div role="status" aria-label="Searching" className="p-2">
              {[0, 1, 2].map((row) => (
                <div key={row} className="flex items-center gap-3 rounded-lg px-2.5 py-2">
                  <div className="skeleton h-6 w-6 rounded-md" />
                  <div className="min-w-0 flex-1 space-y-1.5">
                    <div className="skeleton h-2.5 w-40" />
                    <div className="skeleton h-2 w-64 max-w-full" />
                  </div>
                </div>
              ))}
            </div>
          ) : showEmpty ? (
            <div className="px-3 py-8 text-center">
              <p className="text-xs font-medium text-foreground">No runs, changes or pages match “{debouncedQuery}”</p>
              <p className="mt-1 text-[11px] leading-relaxed text-muted-foreground">
                {search.error
                  ? "Search is unavailable right now. Page shortcuts still work."
                  : "Try a run id, change id, repository or page name."}
              </p>
            </div>
          ) : (
            <>
              {indexedGroups.map((group) => (
                <div key={group.label} role="group" aria-label={group.label}>
                  <p className="px-2 pb-1 pt-2 text-[10px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground/70">
                    {group.label}
                  </p>
                  {group.items.map((item) => {
                    const selected = item.index === active;
                    return (
                      <button
                        key={item.id}
                        id={`${listboxID}-option-${item.index}`}
                        type="button"
                        role="option"
                        aria-selected={selected}
                        data-selected={selected ? "true" : undefined}
                        onMouseEnter={() => setActive(item.index)}
                        onClick={() => activate(item)}
                        className={clsx(
                          "flex w-full items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-left transition-colors",
                          selected ? "bg-muted" : "hover:bg-muted/50",
                        )}
                      >
                        <span
                          className={clsx(
                            "grid h-6 w-6 shrink-0 place-items-center rounded-md border",
                            selected
                              ? "border-border-strong bg-surface text-foreground"
                              : "border-border bg-surface text-muted-foreground",
                          )}
                        >
                          <item.Icon aria-hidden="true" className="h-3 w-3" />
                        </span>
                        <span className="min-w-0 flex-1">
                          <span
                            className={clsx(
                              "block truncate text-xs font-medium leading-4",
                              selected ? "text-foreground" : "text-foreground/90",
                              item.mono && "font-mono text-[11px]",
                            )}
                          >
                            {item.title}
                          </span>
                          {item.subtitle ? (
                            <span className="mt-0.5 block truncate text-[11px] leading-4 text-muted-foreground">
                              {item.subtitle}
                            </span>
                          ) : null}
                        </span>
                        {item.kind === "run" && item.state ? (
                          <StatusPill kind="run" state={item.state} size="xs" className="hidden sm:inline-flex" />
                        ) : null}
                        {item.kind === "change" && item.state ? (
                          <StatusPill kind="change" state={item.state} size="xs" className="hidden sm:inline-flex" />
                        ) : null}
                        {selected ? (
                          <CornerDownLeft aria-hidden="true" className="hidden h-3 w-3 shrink-0 text-muted-foreground sm:block" />
                        ) : null}
                      </button>
                    );
                  })}
                </div>
              ))}
              {!debouncedQuery ? (
                <p className="px-2.5 pb-2 pt-1 text-[11px] leading-relaxed text-muted-foreground">
                  Type to search the run and change index.
                </p>
              ) : null}
            </>
          )}
        </div>

        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-border bg-surface/60 px-3 py-2 text-[10px] text-muted-foreground">
          <span className="flex items-center gap-1">
            <span className="kbd" aria-hidden="true">
              <ArrowUp className="h-3 w-3" />
            </span>
            <span className="kbd" aria-hidden="true">
              <ArrowDown className="h-3 w-3" />
            </span>
            navigate
          </span>
          <span className="flex items-center gap-1">
            <span className="kbd" aria-hidden="true">
              <CornerDownLeft className="h-3 w-3" />
            </span>
            open
          </span>
          <span className="flex items-center gap-1">
            <span className="kbd" aria-hidden="true">
              esc
            </span>
            close
          </span>
          <span className="ml-auto hidden tabular sm:inline">
            {debouncedQuery
              ? search.loading
                ? "searching…"
                : `${items.length} result${items.length === 1 ? "" : "s"}${
                    Number.isFinite(Number(search.data?.duration_ms)) ? ` · ${search.data.duration_ms}ms` : ""
                  }`
              : "Runs · Changes · Pages"}
          </span>
        </div>
      </div>
    </div>
  );
}
