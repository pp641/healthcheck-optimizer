"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import { useApp } from "@/components/app-context";
import { api } from "@/lib/api";
import type { Entry } from "@/lib/types";

/** Key for the virtual node above the scan locations. */
export const ROOT = "";

/**
 * Loads folder listings one level at a time and caches them, so the canvas
 * and tile views can prefetch on hover. The cache resets after every scan or
 * cleanup.
 */
export function useTreeData() {
  const { state, treeVersion } = useApp();
  const cache = useRef(new Map<string, Entry[]>());
  const pending = useRef(new Map<string, Promise<Entry[]>>());
  const [tick, setTick] = useState(0);

  useEffect(() => {
    cache.current.clear();
    pending.current.clear();
    setTick((t) => t + 1);
  }, [treeVersion]);

  const get = useCallback(
    (path: string): Entry[] | undefined => (path === ROOT ? state?.roots : cache.current.get(path)),
    // tick changes whenever a listing arrives, so views memoized on get() redraw.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [state?.roots, tick],
  );

  const load = useCallback((path: string): Promise<Entry[]> => {
    if (path === ROOT) return Promise.resolve([]);
    const hit = cache.current.get(path);
    if (hit) return Promise.resolve(hit);
    let p = pending.current.get(path);
    if (!p) {
      p = api<{ entries: Entry[] }>(`/api/tree?path=${encodeURIComponent(path)}`)
        .then((r) => {
          cache.current.set(path, r.entries);
          setTick((t) => t + 1);
          return r.entries;
        })
        .finally(() => pending.current.delete(path));
      pending.current.set(path, p);
    }
    return p;
  }, []);

  return { get, load };
}

/** Display name: scan locations show as ~/path, loose files get a label. */
export function nameOf(e: Entry, roots: string[] | null | undefined, home: string | null | undefined): string {
  if (e.kind === "files") return e.name.startsWith("+") ? e.name : "loose files";
  if (e.path && roots?.includes(e.path) && home && e.path.startsWith(home + "/")) return "~" + e.path.slice(home.length);
  return e.name;
}

export function isExpandable(e: Entry): boolean {
  return e.kind === "dir" && e.has_children && !e.collapsed && !e.locked && !!e.path;
}
