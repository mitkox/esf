import { createContext, useCallback, useContext, useMemo, useState } from "react";

const WINDOW_OPTIONS = [
  { id: "24h", label: "24 hours" },
  { id: "7d", label: "7 days" },
  { id: "30d", label: "30 days" },
  { id: "90d", label: "90 days" },
  { id: "all", label: "All time" },
];

const WorkspaceContext = createContext(null);

function readStored(key, fallback) {
  try {
    const stored = window.localStorage.getItem(key);
    return stored ? JSON.parse(stored) : fallback;
  } catch {
    return fallback;
  }
}

function writeStored(key, value) {
  try {
    window.localStorage.setItem(key, JSON.stringify(value));
  } catch {
    /* storage may be unavailable */
  }
}

export const DEFAULT_WINDOW = "30d";
export { WINDOW_OPTIONS };

export function WorkspaceProvider({ children }) {
  const [windowKey, setWindowKey] = useState(() => {
    const value = readStored("esf-console-window", DEFAULT_WINDOW);
    return WINDOW_OPTIONS.some((option) => option.id === value) ? value : DEFAULT_WINDOW;
  });
  const [autoRefresh, setAutoRefresh] = useState(() => readStored("esf-console-auto-refresh", true));
  const [refreshNonce, setRefreshNonce] = useState(0);

  const updateWindow = useCallback((value) => {
    setWindowKey(value);
    writeStored("esf-console-window", value);
  }, []);

  const updateAutoRefresh = useCallback((value) => {
    setAutoRefresh(value);
    writeStored("esf-console-auto-refresh", value);
  }, []);

  const refreshAll = useCallback(() => setRefreshNonce((value) => value + 1), []);

  const value = useMemo(
    () => ({ window: windowKey, setWindow: updateWindow, autoRefresh, setAutoRefresh: updateAutoRefresh, refreshNonce, refreshAll }),
    [windowKey, updateWindow, autoRefresh, updateAutoRefresh, refreshNonce, refreshAll],
  );
  return <WorkspaceContext.Provider value={value}>{children}</WorkspaceContext.Provider>;
}

export function useWorkspace() {
  const value = useContext(WorkspaceContext);
  if (!value) throw new Error("useWorkspace must be used inside WorkspaceProvider");
  return value;
}
