import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";

// useApi fetches one console endpoint, cancels in-flight work on unmount or
// dependency change, and polls only while the tab is visible.
export function useApi(path, { params = {}, pollMs = 0, enabled = true, deps = [] } = {}) {
  const [state, setState] = useState({ data: null, error: null, loading: Boolean(enabled), updatedAt: null });
  const [nonce, setNonce] = useState(0);
  const paramsKey = JSON.stringify(params);
  const alive = useRef(true);
  const requestID = useRef(0);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  useEffect(() => {
    if (!enabled) {
      setState((current) => ({ ...current, loading: false }));
      return undefined;
    }
    const controller = new AbortController();
    const id = ++requestID.current;
    let timer;
    let stopped = false;

    const load = async (showSpinner) => {
      if (showSpinner) setState((current) => ({ ...current, loading: true }));
      try {
        const data = await api.get(path, JSON.parse(paramsKey), { signal: controller.signal });
        if (!alive.current || id !== requestID.current) return;
        setState({ data, error: null, loading: false, updatedAt: new Date().toISOString() });
      } catch (error) {
        if (error?.name === "AbortError" || !alive.current || id !== requestID.current) return;
        setState((current) => ({ ...current, error, loading: false }));
      }
      if (!stopped && pollMs > 0) {
        timer = window.setTimeout(() => {
          if (document.visibilityState === "hidden") {
            timer = window.setTimeout(() => load(false), 4000);
          } else {
            load(false);
          }
        }, pollMs);
      }
    };

    load(true);
    return () => {
      stopped = true;
      controller.abort();
      window.clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, paramsKey, enabled, pollMs, nonce, ...deps]);

  const refresh = useCallback(() => setNonce((value) => value + 1), []);
  return { ...state, refresh };
}

export function useDebounced(value, delay = 250) {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(value), delay);
    return () => window.clearTimeout(timer);
  }, [value, delay]);
  return debounced;
}

export function useLocalStorage(key, initial) {
  const [value, setValue] = useState(() => {
    try {
      const stored = window.localStorage.getItem(key);
      return stored === null ? initial : JSON.parse(stored);
    } catch {
      return initial;
    }
  });
  useEffect(() => {
    try {
      window.localStorage.setItem(key, JSON.stringify(value));
    } catch {
      /* storage may be unavailable */
    }
  }, [key, value]);
  return [value, setValue];
}

export function useDocumentTitle(title) {
  useEffect(() => {
    document.title = title ? `${title} · ESF Console` : "ESF Console";
  }, [title]);
}

export function useInterval(callback, delay) {
  const saved = useRef(callback);
  useEffect(() => {
    saved.current = callback;
  }, [callback]);
  useEffect(() => {
    if (!delay) return undefined;
    const id = window.setInterval(() => saved.current(), delay);
    return () => window.clearInterval(id);
  }, [delay]);
}
