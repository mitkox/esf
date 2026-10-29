export function isMacPlatform() {
  if (typeof navigator === "undefined") return false;
  return /mac|iphone|ipad|ipod/i.test(`${navigator.platform || ""} ${navigator.userAgent || ""}`);
}

export function modifierKeyLabel() {
  return isMacPlatform() ? "⌘" : "Ctrl";
}
