import { useEffect, useRef } from "react";
import clsx from "clsx";
import { Activity, ChevronRight, Keyboard, Moon, Sun, X } from "lucide-react";
import { NAV_GROUPS, activeNavItem, hrefFor } from "@/routes";
import { WINDOW_OPTIONS } from "@/lib/workspace";
import { Badge, StatusDot } from "@/components/ui/badge";
import { Select } from "@/components/ui/select";
import { navIcon, NAV_SHORTCUTS } from "./nav-icons";

const ENVIRONMENT_LABEL = {
  production: "Production",
  staging: "Staging",
  local: "Local",
  development: "Development",
  self_hosted: "Self-hosted",
};

function NavList({ activeID, onNavigate }) {
  return (
    <nav aria-label="Primary" className="scroll-thin flex-1 overflow-y-auto px-2.5 py-3">
      {NAV_GROUPS.map((group) => (
        <div key={group.label} className="mb-4 last:mb-1">
          <p className="px-2.5 pb-1 text-[10px] font-semibold uppercase leading-4 tracking-[0.14em] text-muted-foreground/70">
            {group.label}
          </p>
          <ul aria-label={group.label} className="space-y-0.5">
            {group.items.map((item) => {
              const active = activeID === item.id;
              const Icon = navIcon(item.icon);
              const shortcut = NAV_SHORTCUTS[item.id];
              return (
                <li key={item.id}>
                  <a
                    href={hrefFor(item.id)}
                    onClick={onNavigate}
                    aria-current={active ? "page" : undefined}
                    className={clsx(
                      "group relative flex items-center gap-2.5 rounded-lg px-2.5 py-[7px] text-[13px] leading-5 transition-colors",
                      active
                        ? "bg-muted font-medium text-foreground"
                        : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                    )}
                  >
                    {active ? (
                      <span aria-hidden="true" className="absolute inset-y-1.5 left-0 w-[2px] rounded-full bg-primary" />
                    ) : null}
                    <Icon
                      aria-hidden="true"
                      className={clsx(
                        "h-4 w-4 shrink-0 transition-colors",
                        active ? "text-primary" : "text-muted-foreground/80 group-hover:text-foreground",
                      )}
                    />
                    <span className="truncate">{item.label}</span>
                    {shortcut ? (
                      <span
                        aria-hidden="true"
                        className={clsx(
                          "ml-auto hidden shrink-0 items-center gap-0.5 text-[10px] font-medium tabular text-muted-foreground/60 transition-opacity lg:inline-flex",
                          active ? "opacity-100" : "opacity-0 group-hover:opacity-100",
                        )}
                      >
                        <span className="rounded border border-border/70 px-1 leading-4">g</span>
                        <span className="rounded border border-border/70 px-1 leading-4">{shortcut}</span>
                      </span>
                    ) : null}
                  </a>
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </nav>
  );
}

function MobileControls({ theme, onToggleTheme, autoRefresh, onToggleAutoRefresh, windowKey, setWindow }) {
  return (
    <div className="mb-2.5 space-y-2 border-b border-border pb-2.5">
      <Select
        label="Time window"
        ariaLabel="Time window"
        value={windowKey}
        onChange={(event) => setWindow(event.target.value)}
        options={WINDOW_OPTIONS.map((option) => ({ value: option.id, label: option.label }))}
      />
      <div className="flex items-center gap-1">
        <button
          type="button"
          onClick={onToggleAutoRefresh}
          aria-pressed={autoRefresh}
          className="flex h-8 flex-1 items-center gap-2 rounded-lg px-1.5 text-[11px] font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <Activity aria-hidden="true" className={clsx("h-3.5 w-3.5", autoRefresh ? "text-success" : "text-muted-foreground")} />
          <span>Auto-refresh</span>
          <span
            aria-hidden="true"
            className={clsx(
              "relative ml-auto h-4 w-7 rounded-full border transition-colors",
              autoRefresh ? "border-success/40 bg-success/20" : "border-border bg-muted",
            )}
          >
            <span
              className={clsx(
                "absolute top-[3px] h-2.5 w-2.5 rounded-full transition-all",
                autoRefresh ? "left-3.5 bg-success" : "left-[3px] bg-muted-foreground",
              )}
            />
          </span>
        </button>
        <button
          type="button"
          onClick={onToggleTheme}
          aria-label={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
          className="grid h-8 w-8 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          {theme === "dark" ? (
            <Sun aria-hidden="true" className="h-4 w-4" />
          ) : (
            <Moon aria-hidden="true" className="h-4 w-4" />
          )}
        </button>
      </div>
    </div>
  );
}

function SystemCard({ session, health, reviewsPending }) {
  const environment = String(session?.environment || "self-hosted").toLowerCase();
  const environmentLabel = ENVIRONMENT_LABEL[environment] || session?.environment || "Self-hosted";
  const version = session?.factory_version || session?.version || "unknown";
  return (
    <div className="rounded-xl border border-border bg-surface-raised/50 p-2.5">
      <div className="flex items-center gap-1.5">
        <StatusDot tone={health?.tone || "muted"} pulse={health?.tone === "success"} />
        <span className="truncate text-[11px] font-medium leading-4 text-foreground">{health?.label || "Status unknown"}</span>
        <Badge variant="outline" size="xs" className="ml-auto capitalize">
          {environmentLabel}
        </Badge>
      </div>
      <p className="mono mt-1.5 truncate text-[10px] leading-4 text-muted-foreground" title={`factory ${version}`}>
        factory {version}
      </p>
      {reviewsPending > 0 ? (
        <a
          href="#/reviews"
          className="mt-1.5 flex items-center justify-between gap-1 rounded text-[10px] font-medium leading-4 text-warning hover:underline"
        >
          <span>
            {reviewsPending} {reviewsPending === 1 ? "review" : "reviews"} awaiting decision
          </span>
          <ChevronRight aria-hidden="true" className="h-3 w-3" />
        </a>
      ) : null}
    </div>
  );
}

function SidebarContent({
  activeID,
  onNavigate,
  session,
  health,
  reviewsPending,
  theme,
  onToggleTheme,
  autoRefresh,
  onToggleAutoRefresh,
  windowKey,
  setWindow,
  onOpenShortcuts,
  mobile,
  onClose,
  closeRef,
}) {
  return (
    <>
      <div className="flex h-14 shrink-0 items-center gap-2.5 border-b border-border px-3.5">
        <div className="grid h-8 w-8 shrink-0 place-items-center rounded-lg border border-primary/25 bg-primary/10">
          <span className="text-[10px] font-extrabold tracking-tight text-primary">ESF</span>
        </div>
        <div className="min-w-0 flex-1">
          <p className="truncate text-[13px] font-semibold leading-tight tracking-[-0.01em]">Factory Console</p>
          <p className="truncate text-[10px] uppercase tracking-[0.14em] text-muted-foreground">Software factory</p>
        </div>
        {mobile ? (
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            aria-label="Close navigation"
            className="grid h-8 w-8 shrink-0 place-items-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <X aria-hidden="true" className="h-4 w-4" />
          </button>
        ) : null}
      </div>

      <NavList activeID={activeID} onNavigate={onNavigate} />

      <div className="shrink-0 border-t border-border p-2.5 pb-[max(0.625rem,env(safe-area-inset-bottom))]">
        {mobile ? (
          <MobileControls
            theme={theme}
            onToggleTheme={onToggleTheme}
            autoRefresh={autoRefresh}
            onToggleAutoRefresh={onToggleAutoRefresh}
            windowKey={windowKey}
            setWindow={setWindow}
          />
        ) : null}
        <SystemCard session={session} health={health} reviewsPending={reviewsPending} />
        <button
          type="button"
          onClick={onOpenShortcuts}
          className="mt-1 flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-[11px] font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <Keyboard aria-hidden="true" className="h-3.5 w-3.5" />
          Shortcuts
          <span className="kbd ml-auto" aria-hidden="true">
            ?
          </span>
        </button>
      </div>
    </>
  );
}

export function Sidebar(props) {
  const { route, mobileOpen, onClose } = props;
  const activeID = activeNavItem(route.view);
  const closeRef = useRef(null);
  const wasOpen = useRef(false);

  useEffect(() => {
    if (mobileOpen) {
      closeRef.current?.focus?.();
    } else if (wasOpen.current) {
      document.getElementById("console-nav-trigger")?.focus?.();
    }
    wasOpen.current = Boolean(mobileOpen);
  }, [mobileOpen]);

  return (
    <>
      <aside className="fixed inset-y-0 left-0 z-40 hidden w-[248px] flex-col border-r border-border bg-surface lg:flex">
        <SidebarContent {...props} activeID={activeID} mobile={false} />
      </aside>

      {mobileOpen ? (
        <div className="fixed inset-0 z-50 lg:hidden">
          <div
            aria-hidden="true"
            onClick={onClose}
            className="dialog-overlay absolute inset-0 bg-black/50 backdrop-blur-[1px]"
          />
          <div
            role="dialog"
            aria-modal="true"
            aria-label="Navigation"
            className="dialog-panel absolute inset-y-0 left-0 flex w-[248px] max-w-[85vw] flex-col border-r border-border bg-surface shadow-popover"
          >
            <SidebarContent {...props} activeID={activeID} mobile onClose={onClose} closeRef={closeRef} />
          </div>
        </div>
      ) : null}
    </>
  );
}
