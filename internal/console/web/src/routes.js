// Hash routing keeps the SPA fully static: the Go server never needs a
// catch-all rewrite rule and every screen has a shareable deep link.

export const NAV_GROUPS = [
  {
    label: "Factory",
    items: [
      { id: "overview", label: "Overview", icon: "LayoutDashboard" },
      { id: "dispatch", label: "Dispatch", icon: "Send" },
      { id: "runs", label: "Runs", icon: "Play" },
      { id: "changes", label: "Changes", icon: "GitBranch" },
      { id: "reviews", label: "Reviews", icon: "ClipboardCheck" },
    ],
  },
  {
    label: "Governance",
    items: [
      { id: "security", label: "Security", icon: "ShieldCheck" },
      { id: "operations", label: "Operations", icon: "Server" },
      { id: "settings", label: "Settings", icon: "Settings" },
    ],
  },
];

const PAGES = new Set(["overview", "dispatch", "runs", "changes", "reviews", "security", "operations", "settings"]);

// A detail id is a single path segment. Decoding a percent-encoded slash must
// not smuggle a second segment into a page or its API request.
function validDetailID(id) {
  return Boolean(id) && !id.includes("/") && !id.includes("\\") && id !== "." && id !== ".." && id.length <= 200;
}

export function routeFromHash(hash = "") {
  const value = String(hash).replace(/^#\/?/, "").replace(/\/+$/, "");
  if (!value) return { view: "overview", id: "" };
  const [head, ...rest] = value.split("/");
  let id = "";
  try {
    id = rest.length ? decodeURIComponent(rest.join("/")) : "";
  } catch {
    id = "";
  }
  if (head === "runs") return { view: validDetailID(id) ? "run" : "runs", id: validDetailID(id) ? id : "" };
  if (head === "changes") return { view: validDetailID(id) ? "change" : "changes", id: validDetailID(id) ? id : "" };
  return { view: PAGES.has(head) ? head : "overview", id: "" };
}

export function hrefFor(view, id = "") {
  if (view === "run") return `#/runs/${encodeURIComponent(id)}`;
  if (view === "change") return `#/changes/${encodeURIComponent(id)}`;
  return `#/${view}`;
}

export function navigate(view, id = "") {
  const next = hrefFor(view, id);
  if (window.location.hash !== next) window.location.hash = next;
}

export function activeNavItem(view) {
  if (view === "run") return "runs";
  if (view === "change") return "changes";
  return view;
}
