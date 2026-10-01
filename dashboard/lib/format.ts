import type { Device } from "./types";

/** Decimal units, like Finder and Explorer: 1 GB = 1,000,000,000 bytes. */
export function bytes(n: number | null | undefined): string {
  if (n == null) return "—";
  const units = ["B", "kB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function pct(n: number | null | undefined, digits = 0): string {
  return n == null ? "—" : `${n.toFixed(digits)}%`;
}

export function relTime(iso: string | null | undefined): string {
  if (!iso) return "never";
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  const d = Math.floor(s / 86400);
  return d === 1 ? "yesterday" : `${d} days ago`;
}

export function dateTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString("en-GB", { dateStyle: "medium", timeStyle: "short" });
}

export function osLabel(name: string, version: string): string {
  const pretty: Record<string, string> = { macos: "macOS", windows: "Windows", linux: "Linux" };
  return `${pretty[name] ?? name} ${version}`.trim();
}

export type Presence = "online" | "late" | "offline";

/** Online within two reporting intervals, offline after three days. */
export function presence(d: Device, intervalMinutes: number): Presence {
  if (!d.last_seen) return "offline";
  const mins = (Date.now() - new Date(d.last_seen).getTime()) / 60000;
  if (mins <= intervalMinutes * 2 + 10) return "online";
  if (mins <= 72 * 60) return "late";
  return "offline";
}
