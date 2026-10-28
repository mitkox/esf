// Normalises API-provided links into hash-route URLs. The palette and the
// attention menu both accept either "#/runs/x", "/runs/x" or "runs/x" shapes.
export function normalizeHref(href, fallback = "#/overview") {
  if (!href) return fallback;
  const value = String(href).trim();
  if (!value) return fallback;
  if (value.startsWith("#")) return value;
  if (value.startsWith("/")) return `#${value}`;
  return `#/${value}`;
}
