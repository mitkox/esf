import assert from "node:assert/strict";
import test from "node:test";
import { activeNavItem, hrefFor, NAV_GROUPS, routeFromHash } from "./routes.js";

test("routeFromHash resolves every top-level view", () => {
  for (const view of ["overview", "dispatch", "runs", "changes", "reviews", "security", "operations", "settings"]) {
    assert.deepEqual(routeFromHash(`#/${view}`), { view, id: "" });
  }
  assert.deepEqual(routeFromHash(""), { view: "overview", id: "" });
  assert.deepEqual(routeFromHash("#/"), { view: "overview", id: "" });
  assert.deepEqual(routeFromHash("#/nonsense"), { view: "overview", id: "" });
});

test("routeFromHash resolves detail routes and decodes ids", () => {
  assert.deepEqual(routeFromHash("#/runs/run-123"), { view: "run", id: "run-123" });
  assert.deepEqual(routeFromHash("#/changes/change-abc"), { view: "change", id: "change-abc" });
  assert.deepEqual(routeFromHash("#/runs/a%2Fb"), { view: "runs", id: "" });
  assert.deepEqual(routeFromHash("#/runs/a%20b"), { view: "run", id: "a b" });
});

test("hrefFor builds shareable deep links", () => {
  assert.equal(hrefFor("overview"), "#/overview");
  assert.equal(hrefFor("runs"), "#/runs");
  assert.equal(hrefFor("run", "run-1"), "#/runs/run-1");
  assert.equal(hrefFor("change", "change 1"), "#/changes/change%201");
});

test("activeNavItem maps detail views to their parent navigation item", () => {
  assert.equal(activeNavItem("run"), "runs");
  assert.equal(activeNavItem("change"), "changes");
  assert.equal(activeNavItem("overview"), "overview");
  assert.equal(activeNavItem("settings"), "settings");
});

test("navigation exposes every screen exactly once", () => {
  const ids = NAV_GROUPS.flatMap((group) => group.items.map((item) => item.id));
  assert.deepEqual(ids.sort(), ["changes", "dispatch", "operations", "overview", "reviews", "runs", "security", "settings"]);
});
