import {
  ClipboardCheck,
  GitBranch,
  LayoutDashboard,
  Play,
  Send,
  Server,
  Settings,
  ShieldCheck,
} from "lucide-react";

// routes.js keeps navigation icons as plain strings so the router stays
// dependency-free; this map is the single place that resolves them to JSX.
export const NAV_ICONS = {
  LayoutDashboard,
  Play,
  Send,
  GitBranch,
  ClipboardCheck,
  ShieldCheck,
  Server,
  Settings,
};

export function navIcon(name) {
  return NAV_ICONS[name] || LayoutDashboard;
}

export const NAV_SHORTCUTS = {
  overview: "o",
  dispatch: "d",
  runs: "r",
  changes: "c",
  reviews: "v",
  security: "s",
  operations: "p",
  settings: ",",
};
