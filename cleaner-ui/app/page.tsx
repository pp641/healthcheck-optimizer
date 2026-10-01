"use client";

import Link from "next/link";
import { useEffect, useState } from "react";

import { useApp } from "@/components/app-context";
import { useDiskSelection } from "@/components/disk-selection";
import { DiskTree, ItemBadge, RowMenu } from "@/components/tree";
import { TreeCanvas } from "@/components/tree-canvas";
import { Treemap } from "@/components/treemap";
import { ExpandIcon, openBigView, ViewSwitch, VIEW_KEY, views, type View } from "@/components/view-switch";
import { bytes, tilde } from "@/lib/format";

export default function DiskPage() {
  const { state, bumpTree } = useApp();
  const { sel, selected, setSelected, toggle, openFolderPicker, overlays } = useDiskSelection();
  const [view, setView] = useState<View>("canvas");

  useEffect(() => {
    try {
      const v = localStorage.getItem(VIEW_KEY) as View | null;
      if (v && views.some((x) => x.id === v)) setView(v);
    } catch {}
  }, []);
  function pickView(v: View) {
    setView(v);
    try {
      localStorage.setItem(VIEW_KEY, v);
    } catch {}
  }

  if (!state) return null;
  const eligible = state.eligible_items ?? [];
  const firstScan = state.scanning && !state.scanned_at;

  return (
    <>
      <div className="summary card">
        {firstScan ? (
          <div className="hero">
            <span className="hero-label">Looking for old build output and caches…</span>
          </div>
        ) : (
          <div style={{ flex: 1, minWidth: 260 }}>
            <div className="hero">
              <span className="hero-value">{bytes(state.reclaimable ?? 0)}</span>
              <span className="hero-label">can be cleaned safely</span>
            </div>
            <p className="subtle" style={{ marginTop: 6 }}>
              {state.eligible_count ?? 0} {state.eligible_count === 1 ? "item" : "items"}.
              {(state.skipped ?? 0) > 0 &&
                ` ${bytes(state.skipped)} more was found but kept for safety (recent work, uncommitted changes, or tracked by git). Hover a badge to see why.`}
            </p>
          </div>
        )}
        <div className="actions">
          {eligible.length > 0 && (
            <button
              className="btn"
              onClick={() =>
                setSelected(new Map(eligible.map((p) => [p.id, { ...p, item: true, path: p.name.startsWith("/") ? p.name : undefined }])))
              }
            >
              Select all safe items
            </button>
          )}
        </div>
      </div>

      {(state.warnings ?? []).map((w) => (
        <p key={w} className="notice">
          {w}
        </p>
      ))}

      <section className="section">
        <div className="section-head" style={{ justifyContent: "space-between" }}>
          <div>
            <h2>Project folders</h2>
            <p className="subtle">
              Scanning {(state.scan_roots ?? []).map((r) => tilde(r, state.home)).join(", ") || "no folders yet"} ·{" "}
              <Link href="/settings/">manage</Link>
            </p>
          </div>
          <button className="btn" onClick={openFolderPicker} style={{ marginLeft: "auto" }}>
            + Add folder
          </button>
          <ViewSwitch view={view} onPick={pickView} />
          {view !== "list" && (
            <button className="btn big-view-btn" onClick={() => openBigView(view)} title="Open this view in its own tab, using the whole window">
              <ExpandIcon />
              Big view
            </button>
          )}
        </div>
        {view === "canvas" && <TreeCanvas sel={sel} />}
        {view === "tiles" && <Treemap sel={sel} />}
        {view === "list" && (
          <div className="card tree" role="tree" aria-label="Disk usage">
            <DiskTree sel={sel} />
          </div>
        )}
      </section>

      <section className="section">
        <div className="section-head">
          <h2>Caches &amp; tools</h2>
          <p className="subtle">Package caches and tool data outside your project folders.</p>
        </div>
        <div className="card">
          {(state.extras ?? []).length === 0 ? (
            <div className="empty">{state.scanning ? "Checking caches…" : "No caches found."}</div>
          ) : (
            state.extras!.map((it) => (
              <div className="extra" key={it.id}>
                {it.eligible ? (
                  <input
                    type="checkbox"
                    checked={selected.has(it.id)}
                    aria-label={`Select ${it.rule_name}`}
                    onChange={() => toggle({ id: it.id, name: it.path ?? it.rule_name, size: it.size, path: it.path, item: true })}
                  />
                ) : (
                  <span />
                )}
                <div style={{ minWidth: 0 }}>
                  <strong>{it.rule_name}</strong>
                  <div className="detail" title={it.path}>
                    {it.path ? tilde(it.path, state.home) : it.detail}
                  </div>
                </div>
                <ItemBadge item={it} short />
                <span className="size">{bytes(it.size)}</span>
                {it.path ? <RowMenu path={it.path} canRescan={false} onChanged={bumpTree} /> : <span />}
              </div>
            ))
          )}
        </div>
      </section>

      {overlays}
    </>
  );
}
