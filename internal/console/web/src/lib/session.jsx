import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { clearToken, getToken, setToken, verifyToken } from "./api";

const SessionContext = createContext(null);

export function SessionProvider({ children }) {
  const [state, setState] = useState({ status: "loading", session: null, error: "" });

  const load = useCallback(async (token) => {
    setState({ status: "loading", session: null, error: "" });
    try {
      const session = await verifyToken(token);
      setToken(token);
      setState({ status: "authenticated", session, error: "" });
    } catch (error) {
      clearToken();
      setState({
        status: "unauthenticated",
        session: null,
        error: token
          ? error?.status === 401
            ? "That access token was rejected."
            : error?.message || "Unable to verify the session."
          : "",
      });
    }
  }, []);

  useEffect(() => {
    const token = getToken();
    if (!token) {
      // With --no-auth (or a reverse proxy that injects credentials) the
      // server accepts session discovery without a bearer token. Probe it so
      // a fresh browser does not get stuck on the login form; fall back to
      // the form only when the server rejects the anonymous session.
      load("");
      return;
    }
    load(token);
  }, [load]);

  useEffect(() => {
    const onUnauthorized = () => {
      clearToken();
      setState({ status: "unauthenticated", session: null, error: "The session expired. Enter the access token again." });
    };
    window.addEventListener("esf:unauthorized", onUnauthorized);
    return () => window.removeEventListener("esf:unauthorized", onUnauthorized);
  }, []);

  const login = useCallback((token) => load(token.trim()), [load]);
  const logout = useCallback(() => {
    clearToken();
    setState({ status: "unauthenticated", session: null, error: "" });
  }, []);

  const value = useMemo(() => ({ ...state, login, logout }), [state, login, logout]);
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession() {
  const value = useContext(SessionContext);
  if (!value) throw new Error("useSession must be used inside SessionProvider");
  return value;
}
