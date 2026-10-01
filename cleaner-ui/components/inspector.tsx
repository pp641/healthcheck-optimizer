"use client";

import { useApp } from "@/components/app-context";
import type { Selection } from "@/components/tree";
import { ItemBadge } from "@/components/tree";
import { api, errorText } from "@/lib/api";
import { bytes, tilde } from "@/lib/format";
import { isExpandable, nameOf } from "@/lib/tree-data";
import type { Entry } from "@/lib/types";

/** The folder actions every view offers. */
export function useNodeActions() {
  const { toast, startScan, state, bumpTree } = useApp();
  const home = state?.home ?? null;
  const wrap = (fn: () => Promise<unknown>) => async () => {
    try {
      await fn();
    } catch (e) {
      toast(errorText(e));
    }
  };
  return {
    revealLabel: state?.platform === "darwin" ? "Show in Finder" : state?.platform === "windows" ? "Show in Explorer" : "Open folder",
    reveal: (path: string) => wrap(() => api("/api/reveal", { path }))(),
    copy: (path: string) =>
      wrap(async () => {
        await navigator.clipboard.writeText(path);
        toast("Path copied");
      })(),
    rescan: (path: string) => wrap(() => startScan(path))(),
    exclude: (path: string) =>
      wrap(async () => {
        if (!confirm(`Exclude ${tilde(path, home)}?\n\nTidyfleet will never scan or clean it. You can undo this in Settings.`)) return;
        await api("/api/exclude", { path });
        toast("Excluded from scans");
        bumpTree();
      })(),
  };
}

/** Details and actions for the folder picked in the canvas or tile view. */
export function Inspector({ entry, total, sel, onOpen }: { entry: Entry | null; total: number; sel: Selection; onOpen?: (e: Entry) => void }) {
  const { state } = useApp();
  const act = useNodeActions();
  if (!entry) {
    return (
      <aside className="inspector card">
        <p className="subtle">Hover a folder to see its size. Click it to open its branches and see details here.</p>
        <div className="inspector-key">
          <span>
            <span className="key-dot safe" /> safe to clean
          </span>
          <span>
            <span className="key-dot kept" /> matched, kept for safety
          </span>
        </div>
      </aside>
    );
  }
  const item = entry.item;
  const isRoot = !!entry.path && (state?.scan_roots ?? []).includes(entry.path);
  const manageable = entry.kind === "dir" && !!entry.path && !entry.locked && !isRoot;
  const share = total > 0 ? (entry.size / total) * 100 : 0;
  const checked = item ? sel.selected.has(item.id) : false;
  return (
    <aside className="inspector card" aria-live="polite">
      <div>
        <h3 className="inspector-name">{nameOf(entry, state?.scan_roots, state?.home)}</h3>
        {entry.path && <p className="subtle inspector-path">{tilde(entry.path, state?.home ?? null)}</p>}
      </div>
      <dl className="inspector-kv">
        <div>
          <dt>Size</dt>
          <dd>{entry.locked ? "Excluded" : bytes(entry.size)}</dd>
        </div>
        <div>
          <dt>Share of view</dt>
          <dd>{share < 0.1 ? "<0.1" : share.toFixed(share < 10 ? 1 : 0)}%</dd>
        </div>
        <div>
          <dt>Files</dt>
          <dd>{entry.files.toLocaleString()}</dd>
        </div>
      </dl>
      {item && <ItemBadge item={item} />}
      {item?.eligible && <p className="subtle">{item.action}.</p>}
      {entry.collapsed && !item && <p className="subtle">Dependency or VCS folder: measured as a whole, not expanded.</p>}
      {entry.denied && <p className="subtle">Tidyfleet can&apos;t read this folder.</p>}
      <div className="inspector-actions">
        {item?.eligible && (
          <button
            className={`btn ${checked ? "" : "primary"}`}
            onClick={() => sel.toggle({ id: item.id, name: entry.path ?? entry.name, size: item.size })}
          >
            {checked ? "Remove from cleanup" : "Add to cleanup"}
          </button>
        )}
        {!item?.eligible && manageable && (
          <button className="btn" onClick={() => sel.toggleEntry(entry)}>
            {sel.has(entry) ? "Unselect" : "Select"}
          </button>
        )}
        {onOpen && isExpandable(entry) && (
          <button className="btn" onClick={() => onOpen(entry)}>
            Open folder
          </button>
        )}
      </div>
      {manageable && (
        <div className="inspector-actions">
          <button className="btn" onClick={() => sel.manage("move", [entry.path!])}>
            Move to…
          </button>
          <button className="btn danger" onClick={() => sel.manage("trash", [entry.path!])}>
            Move to Trash…
          </button>
        </div>
      )}
      {manageable && <p className="subtle inspector-tip">Tip: drag it onto another folder to move it. ⌘-click to select several.</p>}
      {entry.path && entry.kind === "dir" && (
        <div className="inspector-links">
          <button className="btn link" onClick={() => act.reveal(entry.path!)}>
            {act.revealLabel}
          </button>
          <button className="btn link" onClick={() => act.copy(entry.path!)}>
            Copy path
          </button>
          {!entry.locked && (
            <button className="btn link" onClick={() => act.rescan(entry.path!)}>
              Rescan
            </button>
          )}
          {!entry.locked && (
            <button className="btn link danger" onClick={() => act.exclude(entry.path!)}>
              Exclude
            </button>
          )}
        </div>
      )}
    </aside>
  );
}
