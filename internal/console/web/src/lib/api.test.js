import assert from "node:assert/strict";
import test from "node:test";
import { api, clearToken, getToken, setToken } from "./api.js";

test("bearer survives unavailable storage for reads and raw downloads", async () => {
  const saved = { storage: globalThis.sessionStorage, window: globalThis.window, fetch: globalThis.fetch };
  try {
    globalThis.sessionStorage = {
      getItem() { throw new Error("storage disabled"); },
      setItem() { throw new Error("storage disabled"); },
      removeItem() { throw new Error("storage disabled"); },
    };
    globalThis.window = { location: { origin: "https://console.example" } };
    const headers = [];
    globalThis.fetch = async (_url, options) => {
      headers.push(options.headers.Authorization);
      return new Response("{}", { headers: { "Content-Type": "application/json" } });
    };
    setToken("test-read-token");
    assert.equal(getToken(), "test-read-token");
    await api.get("/overview");
    await api.blob("/runs/example/patch");
    assert.deepEqual(headers, ["Bearer test-read-token", "Bearer test-read-token"]);
    clearToken();
    assert.equal(getToken(), "");
  } finally {
    clearToken();
    globalThis.sessionStorage = saved.storage;
    globalThis.window = saved.window;
    globalThis.fetch = saved.fetch;
  }
});
