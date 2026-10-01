"use client";

import Link from "next/link";
import { useEffect, useState } from "react";

import { useApp } from "@/components/app-context";
import type { Selection } from "@/components/tree";
import { api } from "@/lib/api";
import { bytes, tilde } from "@/lib/format";
import type { Item, Pick } from "@/lib/types";

const SHOW = 8;

function pickOf(it: Item): Pick {
  return { id: it.id, name: it.path ?? it.rule_name, size: it.size, path: it.path, item: true };
}

/** Why an item was kept, in plain words, and what the user can do about it. */
function keptHint(reason = ""): { text: string; settings?: boolean } | null {
  if (reason.includes("uncommitted")) return { text: "Commit or stash your work first, or allow it in Settings.", settings: true };
  if (reason.startsWith("project active")) return { text: "You worked on this project recently. You can change the idle threshold in Settings.", settings: true };
  if (reason.includes("tracked by git") || reason.includes("not gitignored")) return { text: "Git tracks this folder, so it may be your source code." };
  if (reason.includes("not in a git repo")) return { text: "Tidyfleet can't confirm this is build output, so it leaves it alone." };
  if (reason.includes("excluded")) return { text: "It's in your never-touch list.", settings: true };
  return null;
}

/**
 * Every rule match from the last scan in one list: safe items with
 * checkboxes and a Clean button, and the kept ones with the reason.
 */
export function SafeItems({ sel, onClean }: { sel: Selection; onClean: (ids: string[]) => void }) {
  const { state, treeVersion } = useApp();
  const [items, setItems] = useState<Item[] | null>(null);
  const [showAll, setShowAll] = useState(false);
  const home = state?.home ?? null;

  useEffect(() => {
    let live = true;
    api<{ items: Item[] }>("/api/items")
      .then((r) => live && setItems(r.items))
      .catch(() => live && setItems([]));
    return () => {
      live = false;
    };
  }, [treeVersion, state?.scanned_at, state?.eligible_count]);

  if (!items) return null;
  const safe = items.filter((i) => i.eligible);
  const kept = items.filter((i) => !i.eligible);
  const chosen = safe.filter((i) => sel.selected.has(i.id));
  const chosenSize = chosen.reduce((a, i) => a + i.size, 0);
  const keptSize = kept.reduce((a, i) => a + i.size, 0);
  const allOn = safe.length > 0 && chosen.length === safe.length;
  const shown = showAll ? safe : safe.slice(0, SHOW);

  function setAll(on: boolean) {
    for (const it of safe) {
      if (sel.selected.has(it.id) !== on) sel.toggle(pickOf(it));
    }
  }

  return (
    <div className="safe-list">
      {safe.length > 0 && (
        <>
          <div className="safe-head">
            <label className="check">
              <input
                type="checkbox"
                checked={allOn}
                ref={(el) => {
                  if (el) el.indeterminate = chosen.length > 0 && !allOn;
                }}
                onChange={(e) => setAll(e.target.checked)}
                aria-label="Select all safe items"
              />
              <strong>Safe to clean</strong>
              <span className="subtle">({safe.length})</span>
            </label>
            <span className="subtle safe-chosen">
              {chosen.length > 0 ? `${chosen.length} selected · ${bytes(chosenSize)}` : "Tick the items you want to clean"}
            </span>
            <button className="btn primary" disabled={chosen.length === 0} onClick={() => onClean(chosen.map((i) => i.id))}>
              Clean selected{chosen.length > 0 ? ` · ${bytes(chosenSize)}` : ""}
            </button>
          </div>
          <ul className="safe-rows">
            {shown.map((it) => (
              <li key={it.id}>
                <label className={`safe-row${sel.selected.has(it.id) ? " on" : ""}`}>
                  <input type="checkbox" checked={sel.selected.has(it.id)} onChange={() => sel.toggle(pickOf(it))} />
                  <span className="safe-main">
                    <span className="safe-name">{it.rule_name}</span>
                    <span className="safe-path" title={it.path ?? it.detail}>
                      {it.path ? tilde(it.path, home) : it.detail}
                    </span>
                  </span>
                  <span className="safe-meta subtle">
                    {it.kind === "project" && it.stale_days > 0 ? `idle ${it.stale_days} days · ` : ""}
                    {it.action}
                  </span>
                  <span className="safe-size">{bytes(it.size)}</span>
                </label>
              </li>
            ))}
          </ul>
          {safe.length > SHOW && (
            <button className="btn link safe-more" onClick={() => setShowAll(!showAll)}>
              {showAll ? "Show fewer" : `Show all ${safe.length} items`}
            </button>
          )}
        </>
      )}

      {kept.length > 0 && (
        <details className="kept">
          <summary>
            Kept for safety: {bytes(keptSize)} in {kept.length} {kept.length === 1 ? "item" : "items"}. See why
          </summary>
          <ul className="safe-rows">
            {kept.map((it) => {
              const hint = keptHint(it.skip_reason);
              return (
                <li key={it.id} className="kept-row">
                  <span className="kept-lock" aria-hidden>
                    <svg viewBox="0 0 12 12" width="12" height="12">
                      <rect x="2.5" y="5.5" width="7" height="5" rx="1" fill="none" stroke="currentColor" strokeWidth="1.2" />
                      <path d="M4 5.5V4a2 2 0 014 0v1.5" fill="none" stroke="currentColor" strokeWidth="1.2" />
                    </svg>
                  </span>
                  <span className="safe-main">
                    <span className="safe-name">{it.rule_name}</span>
                    <span className="safe-path" title={it.path}>
                      {it.path ? tilde(it.path, home) : it.detail}
                    </span>
                    <span className="kept-reason">
                      Kept: {it.skip_reason}.{" "}
                      {hint && (
                        <span className="subtle">
                          {hint.text} {hint.settings && <Link href="/settings/">Open Settings</Link>}
                        </span>
                      )}
                    </span>
                  </span>
                  <span className="safe-size">{bytes(it.size)}</span>
                </li>
              );
            })}
          </ul>
        </details>
      )}
    </div>
  );
}
