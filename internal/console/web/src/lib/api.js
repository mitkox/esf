// Thin API client for the factory console.
//
// The token is tab-scoped (sessionStorage), attached as a bearer header, never
// placed in a URL and never logged. A 401 clears it through the session layer.

const TOKEN_KEY = "esf-console-token";
const BASE = "/api/v1/console";

export class ApiError extends Error {
  constructor(message, { status = 0, code = "" } = {}) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

export function getToken() {
  try {
    return sessionStorage.getItem(TOKEN_KEY) || "";
  } catch {
    return "";
  }
}

export function setToken(token) {
  try {
    if (token) sessionStorage.setItem(TOKEN_KEY, token);
    else sessionStorage.removeItem(TOKEN_KEY);
  } catch {
    /* storage can be disabled; the in-memory session still works */
  }
}

export function clearToken() {
  setToken("");
}

export function buildURL(path, params = {}) {
  const url = new URL(`${BASE}${path}`, window.location.origin);
  for (const [key, value] of Object.entries(params || {})) {
    if (value === undefined || value === null || value === "") continue;
    if (Array.isArray(value)) {
      for (const item of value) {
        if (item !== undefined && item !== null && item !== "") url.searchParams.append(key, String(item));
      }
      continue;
    }
    url.searchParams.set(key, String(value));
  }
  return url.pathname + url.search;
}

async function request(path, { params, method = "GET", signal, text = false, token } = {}) {
  const headers = { Accept: text ? "text/plain" : "application/json" };
  const bearer = token ?? getToken();
  if (bearer) headers.Authorization = `Bearer ${bearer}`;
  let response;
  try {
    response = await fetch(buildURL(path, params), { method, headers, signal, cache: "no-store" });
  } catch (error) {
    if (error?.name === "AbortError") throw error;
    throw new ApiError("The console server could not be reached.", { code: "network" });
  }
  if (!response.ok) {
    let body = {};
    try {
      body = await response.json();
    } catch {
      /* non-JSON error body */
    }
    const error = new ApiError(body.error || `Request failed (HTTP ${response.status}).`, {
      status: response.status,
      code: body.code || "",
    });
    if (response.status === 401 && !token) {
      window.dispatchEvent(new CustomEvent("esf:unauthorized"));
    }
    throw error;
  }
  if (text) return response.text();
  return response.json();
}

// requestBlob fetches raw bytes for deliverable downloads. Decoding a patch as
// text and re-encoding it would silently replace invalid UTF-8 byte sequences,
// changing the artifact an operator is about to apply.
async function requestBlob(path, { params, signal } = {}) {
  const headers = { Accept: "application/octet-stream" };
  const bearer = getToken();
  if (bearer) headers.Authorization = `Bearer ${bearer}`;
  let response;
  try {
    response = await fetch(buildURL(path, params), { method: "GET", headers, signal, cache: "no-store" });
  } catch (error) {
    if (error?.name === "AbortError") throw error;
    throw new ApiError("The console server could not be reached.", { code: "network" });
  }
  if (!response.ok) {
    let body = {};
    try {
      body = await response.json();
    } catch {
      /* non-JSON error body */
    }
    throw new ApiError(body.error || `Request failed (HTTP ${response.status}).`, {
      status: response.status,
      code: body.code || "",
    });
  }
  return { blob: await response.blob(), headers: response.headers };
}

export const api = {
  get: (path, params, options = {}) => request(path, { params, ...options }),
  text: (path, params, options = {}) => request(path, { params, text: true, ...options }),
  blob: (path, params, options = {}) => requestBlob(path, { params, ...options }),
};

export function verifyToken(candidate) {
  return request("/session", { token: candidate });
}

export default api;
