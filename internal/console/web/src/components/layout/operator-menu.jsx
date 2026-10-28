import clsx from "clsx";
import { ChevronDown, Keyboard, Moon, Sun } from "lucide-react";
import { formatDuration } from "@/lib/format";
import { Avatar } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { useDismiss } from "./use-dismiss";

function capabilityList(session) {
  const capabilities = session?.capabilities;
  if (Array.isArray(capabilities)) return capabilities.map(String);
  if (capabilities && typeof capabilities === "object") {
    return Object.entries(capabilities)
      .filter(([, enabled]) => enabled === true)
      .map(([name]) => name);
  }
  return [];
}

function MetaRow({ label, value, mono = false }) {
  return (
    <div className="flex items-baseline justify-between gap-3 py-1">
      <dt className="shrink-0 text-[11px] leading-4 text-muted-foreground">{label}</dt>
      <dd
        className={clsx(
          "min-w-0 truncate text-[11px] leading-4 text-foreground",
          mono && "font-mono text-[10px]",
        )}
        title={value}
      >
        {value}
      </dd>
    </div>
  );
}

export function OperatorMenu({ open, onOpenChange, session, theme, onToggleTheme, onOpenShortcuts }) {
  const { panelRef, triggerRef } = useDismiss(open, () => onOpenChange(false));
  const operator = session?.operator || {};
  const name = operator.name || "Operator";
  const role = operator.role || "Read-only operator";
  const environment = session?.environment || "self-hosted";
  const capabilities = capabilityList(session);
  const visibleCapabilities = capabilities.slice(0, 4);
  const uptimeSeconds = Number(session?.uptime_seconds);
  const uptimeLabel = Number.isFinite(uptimeSeconds) ? formatDuration(uptimeSeconds * 1000) : "not reported";
  const extraCapabilities = capabilities.length - visibleCapabilities.length;

  return (
    <div className="relative">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => onOpenChange(!open)}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={`Operator menu: ${name}`}
        className="flex h-8 items-center gap-1.5 rounded-lg border border-transparent py-0.5 pl-0.5 pr-1 text-left transition-colors hover:bg-muted"
      >
        <Avatar name={name} size="sm" />
        <span className="hidden max-w-[11rem] truncate text-xs font-medium text-foreground xl:inline">{name}</span>
        <ChevronDown aria-hidden="true" className="hidden h-3.5 w-3.5 text-muted-foreground sm:block" />
      </button>

      {open ? (
        <div
          ref={panelRef}
          role="dialog"
          aria-label="Operator and session details"
          className="dialog-panel fixed inset-x-3 top-[calc(var(--topbar-height)+0.5rem)] z-[60] overflow-hidden rounded-xl border border-border bg-surface-overlay shadow-popover sm:absolute sm:inset-x-auto sm:right-0 sm:top-[calc(100%+0.5rem)] sm:w-[320px]"
        >
          <div className="flex items-start gap-3 border-b border-border px-3.5 py-3">
            <Avatar name={name} size="lg" />
            <div className="min-w-0 flex-1">
              <p className="truncate text-[13px] font-semibold leading-5 text-foreground">{name}</p>
              <p className="truncate text-[11px] leading-4 text-muted-foreground">{role}</p>
              <Badge variant="outline" size="xs" className="mt-1.5 capitalize">
                {environment}
              </Badge>
            </div>
          </div>

          <div className="border-b border-border px-3.5 py-2">
            <dl>
              <MetaRow label="Environment" value={environment} />
              <MetaRow label="Console" value={session?.version || "unknown"} mono />
              <MetaRow label="Factory" value={session?.factory_version || "unknown"} mono />
              <MetaRow label="Server time" value={session?.server_time || "not reported"} mono />
              <MetaRow label="Uptime" value={uptimeLabel} mono />
            </dl>
          </div>

          <div className="border-b border-border px-3.5 py-2.5">
            <p className="text-[10px] font-semibold uppercase leading-4 tracking-[0.12em] text-muted-foreground">
              Capabilities
            </p>
            <div className="mt-1.5 flex flex-wrap gap-1">
              {visibleCapabilities.length ? (
                visibleCapabilities.map((capability) => (
                  <Badge key={capability} variant="muted" size="xs" className="font-mono">
                    {capability}
                  </Badge>
                ))
              ) : (
                <Badge variant="muted" size="xs">
                  read-only
                </Badge>
              )}
              {extraCapabilities > 0 ? (
                <Badge variant="muted" size="xs">
                  +{extraCapabilities}
                </Badge>
              ) : null}
            </div>
          </div>

          <div className="p-1">
            <button
              type="button"
              onClick={() => {
                onOpenChange(false);
                onToggleTheme?.();
              }}
              className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            >
              {theme === "dark" ? (
                <Sun aria-hidden="true" className="h-3.5 w-3.5" />
              ) : (
                <Moon aria-hidden="true" className="h-3.5 w-3.5" />
              )}
              {theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
            </button>
            <button
              type="button"
              onClick={() => {
                onOpenChange(false);
                onOpenShortcuts?.();
              }}
              className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            >
              <Keyboard aria-hidden="true" className="h-3.5 w-3.5" />
              Keyboard shortcuts
              <span className="kbd ml-auto" aria-hidden="true">
                ?
              </span>
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
