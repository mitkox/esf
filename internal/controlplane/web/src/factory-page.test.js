import assert from "node:assert/strict";
import test from "node:test";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { createServer } from "vite";

test("factory preview escapes patch text and sends only the read token", async (context) => {
  const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/#/factory" });
  const names = ["window", "document", "navigator", "sessionStorage", "localStorage", "Event", "MouseEvent", "fetch", "IS_REACT_ACT_ENVIRONMENT"];
  const prior = new Map(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  for (const name of names.slice(0, 7)) {
    Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: dom.window[name] });
  }
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  sessionStorage.setItem("esf-factory-read-token", "separate-read-token");
  const requests = [];
  globalThis.fetch = async (url, options) => {
    requests.push({ url, authorization: options.headers.Authorization });
    if (url.endsWith("/patch")) return { ok: true, text: async () => "<img src=x onerror=alert(1)>" };
    if (url.endsWith("/evidence")) return { ok: true, json: async () => ({ paths: ["run.json", "changes.patch"] }) };
    return { ok: true, json: async () => ({ runs: [{ run_id: "run-1", repository: "repo", patch: "changes.patch", factory_result: "success", conditions: [] }], next_cursor: "" }) };
  };
  const vite = await createServer({ server: { middlewareMode: true, hmr: false }, appType: "custom", logLevel: "silent" });
  const { FactoryPage } = await vite.ssrLoadModule("/src/factory-page.jsx");
  const root = createRoot(document.getElementById("root"));
  context.after(async () => {
    await act(async () => root.unmount());
    await vite.close();
    dom.window.close();
    for (const [name, descriptor] of prior) {
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else delete globalThis[name];
    }
  });
  await act(async () => root.render(React.createElement(FactoryPage, { available: true })));
  await eventually(() => assert.ok([...document.querySelectorAll("button")].some((button) => button.textContent.includes("run-1"))));
  await act(async () => [...document.querySelectorAll("button")].find((button) => button.textContent.includes("run-1")).click());
  await act(async () => [...document.querySelectorAll("button")].find((button) => button.textContent === "Preview").click());
  await eventually(() => assert.match(document.querySelector("pre").textContent, /<img src=x onerror=alert\(1\)>/));
  assert.equal(document.querySelectorAll("img").length, 0);
  assert.deepEqual(requests.map((request) => request.authorization), ["Bearer separate-read-token", "Bearer separate-read-token", "Bearer separate-read-token"]);
  assert.equal(localStorage.getItem("esf-factory-read-token"), null);
});

async function eventually(check) {
  let last;
  for (let attempt = 0; attempt < 50; attempt++) {
    try { check(); return; } catch (error) { last = error; }
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw last;
}
