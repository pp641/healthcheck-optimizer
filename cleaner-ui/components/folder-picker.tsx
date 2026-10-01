"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import { useApp } from "@/components/app-context";
import { api, errorText } from "@/lib/api";
import { tilde } from "@/lib/format";
import type { Config } from "@/lib/types";

type Folder = { name: string; path: string; hidden: boolean; project: boolean; root: boolean; covered: boolean; excluded: boolean };
type Listing = {
  path: string;
  parent: string;
  home: string;
  entries: Folder[];
  shortcuts: { name: string; path: string }[];
  native_picker: boolean;
  scanned: boolean; // the current folder is (inside) a scan location
  excluded: boolean;
};

export type FolderListName = "scan_roots" | "exclude";

/** Adds or removes one folder; returns the saved config. */
export async function changeFolders(list: FolderListName, action: "add" | "remove", path: string) {
  return api<{ config: Config; note?: string }>("/api/folders", { list, action, path });
}

function FolderIcon({ project }: { project: boolean }) {
  return (
    <svg className="fp-icon" viewBox="0 0 16 16" aria-hidden>
      <path d="M1.5 4a1 1 0 011-1h3.6l1.4 1.6h6a1 1 0 011 1V12a1 1 0 01-1 1h-11a1 1 0 01-1-1z" fill={project ? "var(--accent)" : "var(--axis)"} />
    </svg>
  );
}

/**
 * A popup for choosing folders: browse from home, jump to common places, open
 * the system dialog (macOS), or paste a path. With list, "Add" saves to that
 * settings list right away; with choose, picking a folder hands it back
 * (e.g. as a move destination).
 */
export function FolderPicker({
  list = "scan_roots",
  choose,
  title,
  onClose,
}: {
  list?: FolderListName;
  choose?: (path: string) => void;
  title: string;
  onClose: (changed: boolean) => void;
}) {
  const { state } = useApp();
  const ref = useRef<HTMLDialogElement>(null);
  const [dir, setDir] = useState<string>("");
  const [data, setData] = useState<Listing | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [hidden, setHidden] = useState(false);
  const [filter, setFilter] = useState("");
  const [typed, setTyped] = useState("");
  const [added, setAdded] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const home = state?.home ?? null;

  useEffect(() => {
    ref.current?.showModal();
  }, []);

  const go = useCallback(async (path: string) => {
    setErr(null);
    setFilter("");
    try {
      const d = await api<Listing>(`/api/browse?path=${encodeURIComponent(path)}`);
      setData(d);
      setDir(d.path);
    } catch (e) {
      setErr(errorText(e));
    }
  }, []);

  useEffect(() => {
    go("");
  }, [go]);

  async function add(path: string) {
    if (choose) {
      ref.current?.close();
      choose(path);
      return;
    }
    if (list === "scan_roots" && (path === "/" || path === home)) {
      const what = path === "/" ? "the whole disk" : "your whole home folder";
      if (!confirm(`Scan ${what}? That can take a while and needs more permissions. Folders where you keep code are usually enough.`)) return;
    }
    setBusy(true);
    setErr(null);
    try {
      const r = await changeFolders(list, "add", path);
      setAdded((a) => [...a, path]);
      setMsg(`Added ${tilde(path, home)}${r.note ? ` (${r.note})` : ""}`);
      if (data) await go(data.path);
    } catch (e) {
      setErr(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  async function native() {
    setErr(null);
    setMsg("Choose a folder in the window that just opened…");
    try {
      const r = await api<{ path?: string; cancelled?: boolean }>("/api/choose-folder", {
        prompt: choose ? "Choose a destination folder" : list === "scan_roots" ? "Choose a folder to scan" : "Choose a folder Tidyfleet should never touch",
      });
      setMsg(null);
      if (r.path) await add(r.path);
    } catch (e) {
      setMsg(null);
      setErr(errorText(e));
    }
  }

  function close() {
    ref.current?.close();
    onClose(added.length > 0);
  }

  // Breadcrumb segments from the filesystem root.
  const crumbs: { name: string; path: string }[] = [];
  if (dir) {
    let acc = "";
    for (const part of dir.split("/").filter(Boolean)) {
      acc += "/" + part;
      crumbs.push({ name: part, path: acc });
    }
  }
  const shown = (data?.entries ?? []).filter((f) => (hidden || !f.hidden) && f.name.toLowerCase().includes(filter.toLowerCase()));
  const status = (f: Folder): string | null => {
    if (choose) return null;
    if (list === "scan_roots") {
      if (f.root || added.includes(f.path)) return "Scanning";
      if (f.covered) return "Already scanned";
      if (f.excluded) return "Excluded";
    } else if (f.excluded || added.includes(f.path)) return "Excluded";
    return null;
  };
  const currentAdded = !data || (!choose && (added.includes(data.path) || (list === "scan_roots" ? data.scanned : data.excluded)));

  return (
    <dialog ref={ref} className="folder-picker" onCancel={close} aria-labelledby="fp-title">
      <div className="modal-head">
        <h2 id="fp-title">{title}</h2>
        <p className="subtle">
          {choose
            ? "Pick the folder to move into."
            : list === "scan_roots"
              ? "Pick the folders where you keep code. Tidyfleet measures them and looks for old build output inside."
              : "Tidyfleet will never read or clean anything in these folders."}
        </p>
      </div>
      <div className="fp-body">
        <nav className="fp-places" aria-label="Places">
          {data?.shortcuts.map((s) => (
            <button key={s.path} className={`fp-place${s.path === dir ? " active" : ""}`} onClick={() => go(s.path)}>
              {s.name}
            </button>
          ))}
          {data?.native_picker && (
            <button className="btn small fp-native" onClick={native} disabled={busy}>
              Choose in Finder…
            </button>
          )}
        </nav>
        <div className="fp-main">
          <div className="fp-crumbs" aria-label="Current folder">
            <button className="btn link" onClick={() => go("/")}>
              /
            </button>
            {crumbs.map((c, i) => (
              <span key={c.path}>
                {i > 0 && <span className="crumb-sep">/</span>}
                {i === crumbs.length - 1 ? (
                  <strong>{c.name}</strong>
                ) : (
                  <button className="btn link" onClick={() => go(c.path)}>
                    {c.name}
                  </button>
                )}
              </span>
            ))}
          </div>
          <div className="fp-toolbar">
            <input type="text" placeholder="Filter folders" value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="Filter folders" />
            <label className="check">
              <input type="checkbox" checked={hidden} onChange={(e) => setHidden(e.target.checked)} />
              Show hidden
            </label>
          </div>
          <div className="fp-list" role="list">
            {data?.parent && (
              <button className="fp-row" onClick={() => go(data.parent)}>
                <span className="fp-up" aria-hidden>
                  ↰
                </span>
                <span className="fp-name">Up one level</span>
              </button>
            )}
            {!data && !err && <p className="subtle fp-empty">Loading…</p>}
            {data && shown.length === 0 && <p className="subtle fp-empty">No sub-folders here.</p>}
            {shown.map((f) => (
              <div className="fp-row" role="listitem" key={f.path}>
                <button className="fp-open" onClick={() => go(f.path)} aria-label={`Open ${f.name}`}>
                  <FolderIcon project={f.project} />
                  <span className="fp-name">{f.name}</span>
                  {f.project && <span className="badge safe">project</span>}
                </button>
                {status(f) ? (
                  <span className="subtle fp-added">{status(f)}</span>
                ) : (
                  <button className="btn small" disabled={busy} onClick={() => add(f.path)}>
                    {choose ? "Choose" : "Add"}
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>
      </div>
      <form
        className="fp-paste"
        onSubmit={(e) => {
          e.preventDefault();
          if (typed.trim()) add(typed.trim()).then(() => setTyped(""));
        }}
      >
        <input type="text" value={typed} onChange={(e) => setTyped(e.target.value)} placeholder="Or paste a path, e.g. ~/work/projects" aria-label="Folder path" />
        <button className="btn" type="submit" disabled={busy || !typed.trim()}>
          {choose ? "Use path" : "Add path"}
        </button>
      </form>
      <div className="modal-foot">
        <span className={`msg ${err ? "error" : "ok"}`} style={{ marginRight: "auto" }} role="status">
          {err ?? msg}
        </span>
        {!currentAdded && data && (
          <button className="btn" disabled={busy} onClick={() => add(data.path)}>
            {choose ? "Choose" : "Add"} “{tilde(data.path, home)}”
          </button>
        )}
        <button className={`btn ${choose ? "" : "primary"}`} onClick={close}>
          {choose ? "Cancel" : "Done"}
        </button>
      </div>
    </dialog>
  );
}
