// Defensive coercers for the governance screens.
//
// These endpoints are derived from evidence on disk, so any field can be
// absent: arrays may arrive as null (Go nil slices), counts may be missing and
// timestamps may be the zero value. These helpers fail soft so a partial
// payload renders an honest "—"/"Unknown" instead of crashing or inventing a
// healthy zero.

import { formatDateTime, timeAgo } from "@/lib/format";

export function asArray(value) {
  return Array.isArray(value) ? value : [];
}

export function asObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value : {};
}

export function has(object, key) {
  return Object.prototype.hasOwnProperty.call(asObject(object), key);
}

export function asNumber(value) {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

export function asBoolean(value) {
  return typeof value === "boolean" ? value : undefined;
}

export function textOr(value, fallback = "—") {
  if (value === null || value === undefined || value === "") return fallback;
  return String(value);
}

// validTime rejects the Go zero timestamp (0001-01-01) so an undated alert is
// never rendered as a two-thousand-year-old event.
export function validTime(value) {
  if (!value) return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() < 1970) return null;
  return date;
}

export function timeAgoOr(value, fallback = "—") {
  const date = validTime(value);
  return date ? timeAgo(date.toISOString()) : fallback;
}

export function dateTimeOr(value, fallback = "—") {
  return validTime(value) ? formatDateTime(value) : fallback;
}

export function windowInfo(value) {
  const source = asObject(value);
  return {
    key: textOr(source.key, ""),
    label: textOr(source.label, "Selected window"),
    start: validTime(source.start) ? source.start : null,
    end: validTime(source.end) ? source.end : null,
  };
}
