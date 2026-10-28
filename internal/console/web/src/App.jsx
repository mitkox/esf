import React, { Suspense, useEffect, useState } from "react";
import { SessionProvider, useSession } from "./lib/session";
import { WorkspaceProvider } from "./lib/workspace";
import { useDocumentTitle } from "./lib/hooks";
import { routeFromHash } from "./routes";
import { Shell } from "./components/layout/shell";
import { LoginScreen } from "./pages/login";
import { ToastProvider } from "./components/ui/toast";
import { ErrorBoundary } from "./components/error-boundary";

const OverviewPage = React.lazy(() => import("./pages/overview").then((module) => ({ default: module.OverviewPage })));
const DispatchPage = React.lazy(() => import("./pages/dispatch").then((module) => ({ default: module.DispatchPage })));
const RunsPage = React.lazy(() => import("./pages/runs").then((module) => ({ default: module.RunsPage })));
const RunDetailPage = React.lazy(() => import("./pages/run-detail").then((module) => ({ default: module.RunDetailPage })));
const ChangesPage = React.lazy(() => import("./pages/changes").then((module) => ({ default: module.ChangesPage })));
const ChangeDetailPage = React.lazy(() => import("./pages/change-detail").then((module) => ({ default: module.ChangeDetailPage })));
const ReviewsPage = React.lazy(() => import("./pages/reviews").then((module) => ({ default: module.ReviewsPage })));
const SecurityPage = React.lazy(() => import("./pages/security").then((module) => ({ default: module.SecurityPage })));
const OperationsPage = React.lazy(() => import("./pages/operations").then((module) => ({ default: module.OperationsPage })));
const SettingsPage = React.lazy(() => import("./pages/settings").then((module) => ({ default: module.SettingsPage })));

const TITLES = {
  overview: "Overview",
  dispatch: "Dispatch",
  runs: "Runs",
  run: "Run detail",
  changes: "Changes",
  change: "Change detail",
  reviews: "Reviews",
  security: "Security",
  operations: "Operations",
  settings: "Settings",
};

function Page({ route }) {
  useDocumentTitle(TITLES[route.view] || "Overview");
  switch (route.view) {
    case "dispatch":
      return <DispatchPage />;
    case "runs":
      return <RunsPage />;
    case "run":
      return <RunDetailPage runID={route.id} />;
    case "changes":
      return <ChangesPage />;
    case "change":
      return <ChangeDetailPage changeID={route.id} />;
    case "reviews":
      return <ReviewsPage />;
    case "security":
      return <SecurityPage />;
    case "operations":
      return <OperationsPage />;
    case "settings":
      return <SettingsPage />;
    default:
      return <OverviewPage />;
  }
}

function Splash() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background">
      <div className="flex flex-col items-center gap-4">
        <div className="grid h-12 w-12 place-items-center rounded-xl border border-border bg-surface">
          <span className="text-lg font-bold tracking-tight text-primary">ESF</span>
        </div>
        <div className="h-1 w-28 overflow-hidden rounded-full bg-muted">
          <div className="h-full w-1/2 animate-pulse rounded-full bg-primary" />
        </div>
        <p className="text-xs text-muted-foreground">Connecting to the factory…</p>
      </div>
    </div>
  );
}

function Console() {
  const session = useSession();
  const [theme, setTheme] = useState(() => {
    try {
      return window.localStorage.getItem("esf-console-theme") === "light" ? "light" : "dark";
    } catch {
      return "dark";
    }
  });
  const [route, setRoute] = useState(() => routeFromHash(window.location.hash));

  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark");
    try {
      window.localStorage.setItem("esf-console-theme", theme);
    } catch {
      /* storage may be unavailable */
    }
  }, [theme]);

  useEffect(() => {
    const onHashChange = () => setRoute(routeFromHash(window.location.hash));
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  if (session.status === "loading") return <Splash />;
  if (session.status !== "authenticated") {
    return <LoginScreen error={session.error} onLogin={session.login} />;
  }

  return (
    <Shell session={session.session} theme={theme} onToggleTheme={() => setTheme((value) => (value === "dark" ? "light" : "dark"))} route={route}>
      <ErrorBoundary key={`${route.view}:${route.id}`}>
        <Suspense fallback={<PageFallback />}>
          <Page route={route} />
        </Suspense>
      </ErrorBoundary>
    </Shell>
  );
}

function PageFallback() {
  return (
    <div className="mx-auto w-full max-w-[1600px] space-y-4 px-4 py-6 sm:px-6 lg:px-8">
      <div className="skeleton h-8 w-56" />
      <div className="skeleton h-4 w-80" />
      <div className="grid gap-4 pt-4 sm:grid-cols-2 xl:grid-cols-4">
        {[0, 1, 2, 3].map((item) => (
          <div key={item} className="skeleton h-32" />
        ))}
      </div>
      <div className="skeleton h-80" />
    </div>
  );
}

export function App() {
  return (
    <SessionProvider>
      <WorkspaceProvider>
        <ToastProvider>
          <Console />
        </ToastProvider>
      </WorkspaceProvider>
    </SessionProvider>
  );
}
