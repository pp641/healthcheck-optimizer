"use client";

import { useCallback, useState } from "react";

import { useApp } from "@/components/app-context";
import { CleanDialog } from "@/components/clean-dialog";
import { FolderPicker } from "@/components/folder-picker";
import { ManageDialog, type ManageAction } from "@/components/manage-dialog";
import type { Selection } from "@/components/tree";
import { bytes } from "@/lib/format";
import type { Entry, Pick } from "@/lib/types";

/**
 * Everything the Disk page and the big view share: the selection, the
 * selection bar, and the cleanup / move / trash / add-folder dialogs.
 */
export function useDiskSelection() {
  const { startScan } = useApp();
  const [selected, setSelected] = useState<Map<string, Pick>>(new Map());
  const [cleaning, setCleaning] = useState<string[] | null>(null);
  const [managing, setManaging] = useState<{ action: ManageAction; paths: string[]; dest?: string } | null>(null);
  const [picking, setPicking] = useState(false);

  const toggle = useCallback((p: Pick) => {
    setSelected((prev) => {
      const next = new Map(prev);
      if (next.has(p.id)) next.delete(p.id);
      else next.set(p.id, p);
      return next;
    });
  }, []);

  const pickOf = (e: Entry): Pick | null => {
    if (!e.path) return null;
    if (e.item?.eligible) return { id: e.item.id, name: e.path, size: e.item.size, path: e.path, item: true };
    return { id: `path:${e.path}`, name: e.path, size: e.size, path: e.path };
  };

  const sel: Selection = {
    selected,
    toggle,
    toggleEntry: (e) => {
      const p = pickOf(e);
      if (p) toggle(p);
    },
    has: (e) => {
      const p = pickOf(e);
      return !!p && selected.has(p.id);
    },
    dragPaths: (e) => {
      const p = pickOf(e);
      if (p && selected.has(p.id)) return [...selected.values()].flatMap((x) => (x.path ? [x.path] : []));
      return e.path ? [e.path] : [];
    },
    manage: (action, paths, dest) => setManaging({ action, paths, dest }),
  };

  const picks = [...selected.values()];
  const items = picks.filter((p) => p.item);
  const folderPaths = picks.flatMap((p) => (p.path ? [p.path] : []));
  const total = picks.reduce((a, p) => a + p.size, 0);

  const overlays = (
    <>
      {selected.size > 0 && (
        <div className="selection-bar" role="region" aria-label="Selection">
          <span>
            {selected.size} selected · {bytes(total)}
          </span>
          <button className="btn" onClick={() => setSelected(new Map())}>
            Clear
          </button>
          {folderPaths.length > 0 && (
            <>
              <button className="btn" onClick={() => setManaging({ action: "move", paths: folderPaths })}>
                Move to…
              </button>
              <button className="btn danger" onClick={() => setManaging({ action: "trash", paths: folderPaths })}>
                Move to Trash…
              </button>
            </>
          )}
          {items.length > 0 && (
            <button className="btn primary" onClick={() => setCleaning(items.map((p) => p.id))}>
              {items.length === picks.length ? "Preview cleanup" : `Preview cleanup (${items.length} safe)`}
            </button>
          )}
        </div>
      )}
      {managing && (
        <ManageDialog
          action={managing.action}
          paths={managing.paths}
          dest={managing.dest}
          onClose={(changed) => {
            setManaging(null);
            if (changed) setSelected(new Map());
          }}
        />
      )}
      {picking && (
        <FolderPicker
          list="scan_roots"
          title="Add folders to scan"
          onClose={(changed) => {
            setPicking(false);
            if (changed) startScan();
          }}
        />
      )}
      {cleaning && (
        <CleanDialog
          ids={cleaning}
          onClose={(cleaned) => {
            setCleaning(null);
            if (cleaned) setSelected(new Map());
          }}
        />
      )}
    </>
  );

  return {
    sel,
    selected,
    setSelected,
    toggle,
    openFolderPicker: () => setPicking(true),
    previewCleanup: (ids: string[]) => setCleaning(ids),
    overlays,
  };
}
