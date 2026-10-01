"use client";

import { useEffect, useRef, useState } from "react";

import { useApp } from "@/components/app-context";
import { api, errorText } from "@/lib/api";
import { bytes, tilde } from "@/lib/format";
import type { CleanResult, Item } from "@/lib/types";

type Preview = { items: Item[]; total: number; delete_mode: "trash" | "permanent" };

/**
 * Dry run first: shows exactly what will happen to each selected item. Only
 * after confirming does the agent re-scan (so every safety check runs again)
 * and clean.
 */
export function CleanDialog({ ids, onClose }: { ids: string[]; onClose: (cleaned: boolean) => void }) {
  const { state, refresh, bumpTree } = useApp();
  const ref = useRef<HTMLDialogElement>(null);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [result, setResult] = useState<CleanResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [typed, setTyped] = useState("");
  const home = state?.home ?? null;

  useEffect(() => {
    ref.current?.showModal();
    api<Preview>("/api/preview", { ids })
      .then(setPreview)
      .catch((e) => setErr(errorText(e)));
  }, [ids]);

  const permanent = preview?.delete_mode === "permanent";

  async function clean() {
    setBusy(true);
    setErr(null);
    try {
      const r = await api<CleanResult>("/api/clean", { ids, confirm: permanent ? typed : "" });
      setResult(r);
      await refresh();
      bumpTree();
    } catch (e) {
      setErr(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  function close() {
    ref.current?.close();
    onClose(result !== null);
  }

  return (
    <dialog ref={ref} onCancel={(e) => (busy ? e.preventDefault() : close())} aria-labelledby="clean-title">
      {!result ? (
        <>
          <div className="modal-head">
            <h2 id="clean-title">Preview cleanup</h2>
            <p className="subtle">Dry run: nothing has changed yet.</p>
          </div>
          <div className="modal-body">
            {!preview && !err && <p className="subtle">Loading…</p>}
            {preview && preview.items.length === 0 && <p>None of the selected items can be cleaned any more. Rescan and try again.</p>}
            {preview && preview.items.length > 0 && (
              <table>
                <thead>
                  <tr>
                    <th>Item</th>
                    <th>What happens</th>
                    <th className="num">Frees</th>
                  </tr>
                </thead>
                <tbody>
                  {preview.items.map((it) => (
                    <tr key={it.id}>
                      <td className="path">
                        <strong>{it.rule_name}</strong>
                        <div className="subtle">{it.path ? tilde(it.path, home) : it.detail}</div>
                      </td>
                      <td>{it.action}</td>
                      <td className="num">{bytes(it.size)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
          <div className="modal-foot">
            {err && <span className="msg error" style={{ marginRight: "auto" }}>{err}</span>}
            {permanent && preview && preview.items.length > 0 && (
              <label className="field" style={{ marginRight: "auto" }}>
                Delete mode is permanent. Type &quot;delete&quot; to confirm
                <input type="text" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
              </label>
            )}
            <button className="btn" onClick={close} disabled={busy}>
              Cancel
            </button>
            <button
              className="btn primary"
              onClick={clean}
              disabled={busy || !preview || preview.items.length === 0 || (permanent && typed !== "delete")}
            >
              {busy ? "Cleaning…" : permanent ? `Delete ${bytes(preview?.total)}` : `Clean ${preview?.items.length ?? ""} items · ${bytes(preview?.total)}`}
            </button>
          </div>
        </>
      ) : (
        <>
          <div className="modal-head">
            <h2 id="clean-title">Freed about {bytes(result.freed)}</h2>
            <p className="subtle">
              {result.delete_mode === "permanent" ? "Deleted permanently." : "Folders are in the Trash until you empty it; restore them from there if needed."}
            </p>
          </div>
          <div className="modal-body">
            <table>
              <tbody>
                {result.entries.map((e, i) => (
                  <tr key={i}>
                    <td>{e.ok ? "✓" : "✗"}</td>
                    <td className="path">
                      {e.path ? tilde(e.path, home) : e.rule_id}
                      {!e.ok && <div className="msg error">{e.error}</div>}
                    </td>
                    <td className="num">{bytes(e.bytes)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {result.skipped.length > 0 && (
              <div className="notice">
                <strong>Not cleaned, because things changed since the preview:</strong>
                <ul>
                  {result.skipped.map((s, i) => (
                    <li key={i}>{s}</li>
                  ))}
                </ul>
              </div>
            )}
          </div>
          <div className="modal-foot">
            <button className="btn primary" onClick={close}>
              Done
            </button>
          </div>
        </>
      )}
    </dialog>
  );
}
