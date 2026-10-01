"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import { useApp } from "@/components/app-context";
import { FolderPicker } from "@/components/folder-picker";
import { api, errorText } from "@/lib/api";
import { bytes, tilde } from "@/lib/format";

export type ManageAction = "trash" | "move";

type Item = { path: string; name: string; size: number; warnings: string[]; blocked?: string; target?: string };
type Result = { path: string; to?: string; ok: boolean; error?: string; size: number };

function parentOf(p: string) {
  const i = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\"));
  return i > 0 ? p.slice(0, i) : p;
}

/** Undoes moves by putting each folder back where it came from. */
export async function undoMoves(results: Result[]) {
  const byParent = new Map<string, string[]>();
  for (const r of results) {
    if (!r.ok || !r.to) continue;
    const back = parentOf(r.path);
    byParent.set(back, [...(byParent.get(back) ?? []), r.to]);
  }
  for (const [dest, paths] of byParent) {
    await api("/api/manage", { action: "move", paths, dest });
  }
}

/**
 * Confirms a hand-picked Trash or Move. The server re-checks everything when
 * it acts; this shows what will happen and anything worth a second look.
 */
export function ManageDialog({
  action,
  paths,
  dest: initialDest,
  onClose,
}: {
  action: ManageAction;
  paths: string[];
  dest?: string;
  onClose: (changed: boolean) => void;
}) {
  const { state, toast, refresh, bumpTree } = useApp();
  const ref = useRef<HTMLDialogElement>(null);
  const [dest, setDest] = useState(initialDest ?? "");
  const [items, setItems] = useState<Item[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [ack, setAck] = useState(false);
  const [results, setResults] = useState<Result[] | null>(null);
  const [choosing, setChoosing] = useState(action === "move" && !initialDest);
  const home = state?.home ?? null;

  // For a move without a destination, the folder picker comes first; this
  // dialog opens once there is something to confirm.
  useEffect(() => {
    if (!choosing && ref.current && !ref.current.open) ref.current.showModal();
  }, [choosing]);

  const load = useCallback(async () => {
    if (action === "move" && !dest) return;
    setErr(null);
    setItems(null);
    try {
      const r = await api<{ items: Item[] }>("/api/manage/preview", { action, paths, dest });
      setItems(r.items);
    } catch (e) {
      setErr(errorText(e));
    }
  }, [action, paths, dest]);
  useEffect(() => {
    load();
  }, [load]);

  const ready = (items ?? []).filter((i) => !i.blocked);
  const blocked = (items ?? []).filter((i) => i.blocked && !i.blocked.startsWith("already included"));
  const total = ready.reduce((a, i) => a + i.size, 0);
  const risky = ready.some((i) => i.warnings.some((w) => w.includes("uncommitted") || w.includes("git repositor")));

  async function run() {
    setBusy(true);
    setErr(null);
    try {
      const r = await api<{ results: Result[] }>("/api/manage", { action, paths: ready.map((i) => i.path), dest });
      setResults(r.results);
      bumpTree();
      refresh();
      const ok = r.results.filter((x) => x.ok);
      if (ok.length > 0 && r.results.every((x) => x.ok)) {
        close(true);
        if (action === "move") {
          toast(`Moved ${ok.length} ${ok.length === 1 ? "folder" : "folders"} to ${tilde(dest, home)}`, {
            label: "Undo",
            run: () =>
              undoMoves(ok)
                .then(() => {
                  toast("Move undone");
                  bumpTree();
                  refresh();
                })
                .catch((e) => toast(errorText(e))),
          });
        } else {
          toast(`Moved ${ok.length} ${ok.length === 1 ? "folder" : "folders"} (${bytes(ok.reduce((a, x) => a + x.size, 0))}) to the Trash`);
        }
      }
    } catch (e) {
      setErr(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  function close(changed: boolean) {
    ref.current?.close();
    onClose(changed);
  }

  const verb = action === "trash" ? "Move to Trash" : "Move";
  return (
    <>
      <dialog ref={ref} onCancel={(e) => (busy ? e.preventDefault() : close(results !== null))} aria-labelledby="manage-title">
        <div className="modal-head">
          <h2 id="manage-title">
            {results ? "Done, with problems" : action === "trash" ? "Move folders to the Trash?" : "Move folders?"}
          </h2>
          <p className="subtle">
            {action === "trash"
              ? "You chose these by hand, so they may be your own work. They go to the Trash and can be restored from there."
              : "Folders keep everything inside them. You can undo right after moving."}
          </p>
        </div>
        <div className="modal-body">
          {action === "move" && (
            <div className="dest-row">
              <span className="subtle">Destination</span>
              <strong className="mono">{dest ? tilde(dest, home) : "not chosen"}</strong>
              <button className="btn small" onClick={() => setChoosing(true)} disabled={busy}>
                {dest ? "Change…" : "Choose…"}
              </button>
            </div>
          )}
          {!items && !err && (action !== "move" || dest) && <p className="subtle">Checking…</p>}
          {results ? (
            <table>
              <tbody>
                {results.map((r) => (
                  <tr key={r.path}>
                    <td>{r.ok ? "✓" : "✗"}</td>
                    <td className="path">
                      {tilde(r.path, home)}
                      {r.error && <div className="msg error">{r.error}</div>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            items && (
              <table>
                <tbody>
                  {items.map((it) => (
                    <tr key={it.path} className={it.blocked ? "blocked-row" : undefined}>
                      <td className="path">
                        <strong>{it.name}</strong>
                        <div className="subtle">{tilde(it.path, home)}</div>
                        {it.blocked ? (
                          <div className="msg error">Can&apos;t {action === "trash" ? "trash" : "move"}: {it.blocked}</div>
                        ) : (
                          it.warnings.map((w) => (
                            <div key={w} className="warn-line">
                              ⚠ {w}
                            </div>
                          ))
                        )}
                      </td>
                      <td className="num">{bytes(it.size)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )
          )}
        </div>
        <div className="modal-foot">
          {err && (
            <span className="msg error" style={{ marginRight: "auto" }}>
              {err}
            </span>
          )}
          {!results && risky && (
            <label className="check" style={{ marginRight: "auto" }}>
              <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} />
              I understand this includes git repositories or unsaved work
            </label>
          )}
          <button className="btn" onClick={() => close(results !== null)} disabled={busy}>
            {results ? "Close" : "Cancel"}
          </button>
          {!results && (
            <button
              className={`btn ${action === "trash" ? "danger-fill" : "primary"}`}
              onClick={run}
              disabled={busy || ready.length === 0 || (risky && !ack)}
            >
              {busy ? "Working…" : `${verb} ${ready.length} ${ready.length === 1 ? "folder" : "folders"}${total ? ` · ${bytes(total)}` : ""}`}
            </button>
          )}
          {!results && blocked.length > 0 && ready.length > 0 && (
            <span className="subtle" style={{ width: "100%", textAlign: "right" }}>
              {blocked.length} blocked {blocked.length === 1 ? "folder is" : "folders are"} skipped.
            </span>
          )}
        </div>
      </dialog>
      {choosing && (
        <FolderPicker
          choose={(p) => {
            setDest(p);
            setChoosing(false);
          }}
          title="Move to…"
          onClose={() => {
            setChoosing(false);
            if (!dest) close(false);
          }}
        />
      )}
    </>
  );
}
