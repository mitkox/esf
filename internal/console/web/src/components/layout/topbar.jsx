import clsx from "clsx";
import { Activity, ChevronRight, Menu, Moon, RefreshCw, Search, Sun } from "lucide-react";
import { NAV_GROUPS, activeNavItem, hrefFor } from "@/routes";
import { WINDOW_OPTIONS } from "@/lib/workspace";
import { Select } from "@/components/ui/select";
import { AttentionMenu } from "./attention-menu";
import { OperatorMenu } from "./operator-menu";
import { modifierKeyLabel } from "./platform";

function Breadcrumbs({ route }) {
  const activeID = activeNavItem(route.view);
  const group = NAV_GROUPS.find((candidate) => candidate.items.some((item) => item.id === activeID));
  const item = group?.items.find((candidate) => candidate.id === activeID);
  const detail = route.view === "run" || route.view === "change";
  const label = item?.label || "Overview";
  return (
    <>
      <nav aria-label="Breadcrumb" className="hidden min-w-0 md:block">
        <ol className="flex min-w-0 items-center gap-1.5 text-xs leading-5">
          {group ? <li className="shrink-0 text-muted-foreground">{group.label}</li> : null}
          <li aria-hidden="true" className="grid shrink-0 place-items-center text-muted-foreground/40">
            <ChevronRight className="h-3 w-3" />
          </li>
          <li className="min-w-0">
            {detail ? (
              <a href={hrefFor(activeID)} className="block truncate text-muted-foreground transition-colors hover:text-foreground">
                {label}
              </a>
            ) : (
              <span aria-current="page" className="block truncate font-medium text-foreground">
                {label}
              </span>
            )}
          </li>
          {detail ? (
            <>
              <li aria-hidden="true" className="grid shrink-0 place-items-center text-muted-foreground/40">
                <ChevronRight className="h-3 w-3" />
              </li>
              <li className="min-w-0">
                <span className="mono block max-w-[180px] truncate leading-5 text-foreground" title={route.id}>
                  {route.id}
                </span>
              </li>
            </>
          ) : null}
        </ol>
      </nav>
      <span className="min-w-0 truncate text-[13px] font-semibold text-foreground md:hidden">{label}</span>
    </>
  );
}

export function Topbar({
  session,
  theme,
  onToggleTheme,
  route,
  windowKey,
  setWindow,
  autoRefresh,
  setAutoRefresh,
  onRefresh,
  refreshing,
  updatedLabel,
  onOpenPalette,
  onOpenNav,
  onOpenShortcuts,
  attentionOpen,
  setAttentionOpen,
  operatorOpen,
  setOperatorOpen,
  overview,
  health,
  attentionCount,
  reviews,
}) {
  const modifier = modifierKeyLabel();
  const shortcut = modifier === "⌘" ? "⌘K" : "Ctrl K";

  return (
    <header className="sticky top-0 z-40 h-[var(--topbar-height)] shrink-0 border-b border-border">
      <div aria-hidden="true" className="chrome-blur absolute inset-0" />
      {refreshing ? (
        <div aria-hidden="true" className="absolute inset-x-0 bottom-0 h-px overflow-hidden bg-primary/20">
          <div className="h-full w-1/3 animate-pulse rounded-full bg-primary/70" />
        </div>
      ) : null}

      <div className="relative flex h-full items-center gap-2 px-3 sm:px-6 lg:px-8">
        <button
          type="button"
          id="console-nav-trigger"
          onClick={onOpenNav}
          aria-label="Open navigation"
          className="grid h-8 w-8 shrink-0 place-items-center rounded-lg border border-transparent text-muted-foreground transition-colors hover:bg-muted hover:text-foreground lg:hidden"
        >
          <Menu aria-hidden="true" className="h-4 w-4" />
        </button>

        <div className="min-w-0 flex-1 md:flex-none">
          <Breadcrumbs route={route} />
        </div>

        <div className="ml-auto flex shrink-0 items-center gap-0.5 sm:gap-1">
          <button
            type="button"
            onClick={onOpenPalette}
            aria-label="Open command palette"
            aria-keyshortcuts="Meta+K Control+K"
            className="flex h-8 items-center gap-2 rounded-lg border border-border bg-surface/70 px-3 text-xs text-muted-foreground transition-colors hover:border-border-strong hover:text-foreground"
          >
            <Search aria-hidden="true" className="h-3.5 w-3.5" />
            <span className="hidden xl:inline">Jump to…</span>
            <span className="kbd hidden border-border/80 sm:inline-flex" aria-hidden="true">
              {shortcut}
            </span>
          </button>

          <span aria-hidden="true" className="mx-0.5 hidden h-5 w-px bg-border lg:block" />

          <Select
            className="hidden w-[132px] lg:block"
            ariaLabel="Time window"
            value={windowKey}
            onChange={(event) => setWindow(event.target.value)}
            options={WINDOW_OPTIONS.map((option) => ({ value: option.id, label: option.label }))}
          />

          <button
            type="button"
            onClick={onRefresh}
            aria-label="Refresh console data"
            title={updatedLabel ? `Last updated ${updatedLabel}` : "Not yet synced"}
            className="flex h-8 items-center gap-1.5 rounded-lg border border-transparent px-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground xl:px-2"
          >
            <RefreshCw aria-hidden="true" className={clsx("h-3.5 w-3.5", refreshing && "animate-spin")} />
            <span className="tabular hidden text-[11px] xl:inline">
              {updatedLabel ? `Updated ${updatedLabel}` : "Not yet synced"}
            </span>
          </button>

          <button
            type="button"
            onClick={() => setAutoRefresh(!autoRefresh)}
            aria-pressed={autoRefresh}
            aria-label={autoRefresh ? "Disable auto-refresh" : "Enable auto-refresh"}
            title={autoRefresh ? "Auto-refresh is on (30s)" : "Auto-refresh is off"}
            className={clsx(
              "relative hidden h-8 w-8 place-items-center rounded-lg border border-transparent transition-colors hover:bg-muted sm:grid",
              autoRefresh ? "text-success" : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Activity aria-hidden="true" className="h-4 w-4" />
            {autoRefresh ? (
              <span aria-hidden="true" className="absolute right-1.5 top-1.5 h-1.5 w-1.5 animate-pulse rounded-full bg-success" />
            ) : null}
          </button>

          <AttentionMenu
            open={attentionOpen}
            onOpenChange={setAttentionOpen}
            data={overview?.data}
            loading={overview?.loading}
            error={overview?.error}
            onRetry={overview?.refresh}
            health={health}
            attentionCount={attentionCount}
            reviews={reviews}
          />

          <button
            type="button"
            onClick={onToggleTheme}
            aria-label={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
            title={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
            className="hidden h-8 w-8 place-items-center rounded-lg border border-transparent text-muted-foreground transition-colors hover:bg-muted hover:text-foreground sm:grid"
          >
            {theme === "dark" ? <Sun aria-hidden="true" className="h-4 w-4" /> : <Moon aria-hidden="true" className="h-4 w-4" />}
          </button>

          <span aria-hidden="true" className="mx-0.5 hidden h-5 w-px bg-border sm:block" />

          <OperatorMenu
            open={operatorOpen}
            onOpenChange={setOperatorOpen}
            session={session}
            theme={theme}
            onToggleTheme={onToggleTheme}
            onOpenShortcuts={onOpenShortcuts}
          />
        </div>
      </div>
    </header>
  );
}
