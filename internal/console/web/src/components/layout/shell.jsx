import { useCallback, useEffect, useRef, useState } from "react";
import { useApi } from "@/lib/hooks";
import { timeAgo } from "@/lib/format";
import { useWorkspace } from "@/lib/workspace";
import { hrefFor } from "@/routes";
import { Sidebar } from "./sidebar";
import { Topbar } from "./topbar";
import { CommandPalette } from "./command-palette";
import { ShortcutsDialog } from "./shortcuts-dialog";

const GO_VIEWS = {
  o: "overview",
  d: "dispatch",
  r: "runs",
  c: "changes",
  v: "reviews",
  s: "security",
  p: "operations",
  ",": "settings",
};

// GET /overview -> data.health.status is one of three values.
const HEALTH = {
  operational: { label: "Factory healthy", tone: "success" },
  degraded: { label: "Factory degraded", tone: "warning" },
  attention: { label: "Factory needs attention", tone: "danger" },
};

function healthMeta(data, error, loading) {
  if (error) return { label: "Status unavailable", tone: "muted" };
  const status = String(data?.health?.status || "").toLowerCase();
  if (!status) {
    if (!data && loading) return { label: "Checking status…", tone: "muted" };
    return { label: "Health not reported", tone: "muted" };
  }
  if (HEALTH[status]) return HEALTH[status];
  return { label: `Factory ${status}`, tone: "warning" };
}

function isTypingTarget(target) {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || target.isContentEditable;
}

// Shell owns the persistent console chrome: navigation, top bar, global keys
// and the shell-level overview poll that feeds the attention bell and system
// status. It never imports page modules; screens arrive as children.
export function Shell({ session, theme, onToggleTheme, route, children }) {
  const { window: windowKey, setWindow, autoRefresh, setAutoRefresh, refreshAll } = useWorkspace();
  const [overlay, setOverlay] = useState(null);
  const [attentionOpen, setAttentionOpen] = useState(false);
  const [operatorOpen, setOperatorOpen] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const goPending = useRef({ active: false, timer: 0 });

  const overview = useApi("/overview", {
    params: { window: windowKey },
    pollMs: autoRefresh ? 30000 : 0,
  });

  const attention = Array.isArray(overview.data?.attention) ? overview.data.attention : [];
  // KPI reviews block: {value, oldest_wait_ms, paused_live}
  const reviews = overview.data?.kpis?.reviews || {};
  const reviewsPending = Number.isFinite(Number(reviews.value)) ? Number(reviews.value) : 0;
  const attentionCount = attention.length || reviewsPending;
  const health = healthMeta(overview.data, overview.error, overview.loading);
  const updatedLabel = overview.updatedAt ? timeAgo(overview.updatedAt, now) : "";

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 15000);
    return () => window.clearInterval(timer);
  }, []);

  const handleRefresh = useCallback(() => {
    overview.refresh();
    refreshAll();
  }, [overview.refresh, refreshAll]);

  const routeKey = `${route.view}:${route.id}`;
  useEffect(() => {
    setOverlay(null);
    setAttentionOpen(false);
    setOperatorOpen(false);
  }, [routeKey]);

  useEffect(() => {
    const pending = goPending.current;
    return () => window.clearTimeout(pending.timer);
  }, []);

  // Body scroll lock for full-screen overlays (drawer, palette, shortcuts).
  const modalOpen = overlay !== null;
  useEffect(() => {
    if (!modalOpen) return undefined;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previous;
    };
  }, [modalOpen]);

  useEffect(() => {
    const onKeyDown = (event) => {
      const key = event.key;
      if ((event.metaKey || event.ctrlKey) && key.toLowerCase() === "k") {
        event.preventDefault();
        setAttentionOpen(false);
        setOperatorOpen(false);
        setOverlay((current) => (current === "palette" ? null : "palette"));
        return;
      }
      if (key === "Escape") {
        setOverlay(null);
        setAttentionOpen(false);
        setOperatorOpen(false);
        goPending.current.active = false;
        return;
      }
      if (overlay || attentionOpen || operatorOpen) return;
      if (isTypingTarget(event.target)) {
        goPending.current.active = false;
        return;
      }
      if (key === "/") {
        event.preventDefault();
        setOverlay("palette");
        return;
      }
      if (key === "?") {
        event.preventDefault();
        setOverlay("shortcuts");
        return;
      }
      if (key.toLowerCase() === "g") {
        const pending = goPending.current;
        pending.active = true;
        window.clearTimeout(pending.timer);
        pending.timer = window.setTimeout(() => {
          pending.active = false;
        }, 1200);
        return;
      }
      if (goPending.current.active) {
        const view = GO_VIEWS[key.toLowerCase()];
        goPending.current.active = false;
        window.clearTimeout(goPending.current.timer);
        if (view) {
          event.preventDefault();
          const next = hrefFor(view);
          if (window.location.hash !== next) window.location.hash = next;
        }
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [overlay, attentionOpen, operatorOpen]);

  const closeOverlay = useCallback(() => setOverlay(null), []);

  return (
    <div className="min-h-screen bg-background text-foreground">
      <a
        href="#main-content"
        className="skip-link"
        onClick={(event) => {
          event.preventDefault();
          const main = document.getElementById("main-content");
          if (main) {
            main.focus();
            main.scrollIntoView({ block: "start" });
          }
        }}
      >
        Skip to main content
      </a>

      <div aria-hidden="true" className="glow-top pointer-events-none fixed inset-x-0 top-0 z-0 h-72" />

      <Sidebar
        route={route}
        session={session}
        health={health}
        reviewsPending={reviewsPending}
        theme={theme}
        onToggleTheme={onToggleTheme}
        autoRefresh={autoRefresh}
        onToggleAutoRefresh={() => setAutoRefresh(!autoRefresh)}
        windowKey={windowKey}
        setWindow={setWindow}
        onOpenShortcuts={() => setOverlay("shortcuts")}
        mobileOpen={overlay === "nav"}
        onClose={closeOverlay}
      />

      <div className="relative z-10 flex min-h-screen flex-col lg:pl-[248px]">
        <Topbar
          session={session}
          theme={theme}
          onToggleTheme={onToggleTheme}
          route={route}
          windowKey={windowKey}
          setWindow={setWindow}
          autoRefresh={autoRefresh}
          setAutoRefresh={setAutoRefresh}
          onRefresh={handleRefresh}
          refreshing={overview.loading}
          updatedLabel={updatedLabel}
          onOpenPalette={() => setOverlay("palette")}
          onOpenNav={() => setOverlay("nav")}
          onOpenShortcuts={() => setOverlay("shortcuts")}
          attentionOpen={attentionOpen}
          setAttentionOpen={setAttentionOpen}
          operatorOpen={operatorOpen}
          setOperatorOpen={setOperatorOpen}
          overview={overview}
          health={health}
          attentionCount={attentionCount}
          reviews={reviews}
        />

        <main id="main-content" tabIndex={-1} className="min-w-0 flex-1 outline-none">
          <div className="content-shell">{children}</div>
        </main>
      </div>

      <CommandPalette open={overlay === "palette"} onClose={closeOverlay} />
      <ShortcutsDialog open={overlay === "shortcuts"} onClose={closeOverlay} />
    </div>
  );
}
