"use client";

import { useEffect, useState } from "react";

import { useApp } from "@/components/app-context";
import { useDiskSelection } from "@/components/disk-selection";
import { TreeCanvas } from "@/components/tree-canvas";
import { Treemap } from "@/components/treemap";
import { ExpandIcon, ViewSwitch, type View } from "@/components/view-switch";
import { bytes, relTime } from "@/lib/format";

/** Tree or Tiles on their own, filling the whole window (opened in a new tab). */
export default function ExplorePage() {
  const { state, startScan } = useApp();
  const { sel, openFolderPicker, overlays } = useDiskSelection();
  const [view, setView] = useState<View>("canvas");
  const [full, setFull] = useState(false);

  useEffect(() => {
    const v = new URLSearchParams(window.location.search).get("view");
    if (v === "tiles" || v === "canvas") setView(v);
    const onFs = () => setFull(!!document.fullscreenElement);
    document.addEventListener("fullscreenchange", onFs);
    return () => document.removeEventListener("fullscreenchange", onFs);
  }, []);

  useEffect(() => {
    document.title = `${view === "tiles" ? "Tiles" : "Tree"} · Tidyfleet Cleaner`;
  }, [view]);

  function pickView(v: View) {
    setView(v);
    history.replaceState(null, "", `?view=${v}`);
  }

  function toggleFull() {
    if (document.fullscreenElement) document.exitFullscreen();
    else document.documentElement.requestFullscreen().catch(() => {});
  }

  return (
    <div className="explore">
      <header className="explore-bar">
        <div className="brand">
          <span className="brand-mark" aria-hidden>
            T
          </span>
          Tidyfleet <span className="tag">Big view</span>
        </div>
        <ViewSwitch view={view} onPick={pickView} only={["canvas", "tiles"]} />
        {state?.reclaimable != null && (
          <span className="subtle explore-stat">
            <strong>{bytes(state.reclaimable)}</strong> safe to clean
          </span>
        )}
        <div className="explore-actions">
          <span className="subtle" aria-live="polite">
            {state?.scanning ? (
              <span className="row-inline">
                <span className="spinner" aria-hidden /> Scanning… {state.visited.toLocaleString()} folders
              </span>
            ) : (
              `Scanned ${relTime(state?.scanned_at)}`
            )}
          </span>
          {!state?.scanning && (
            <button className="btn small" onClick={() => startScan()}>
              Rescan
            </button>
          )}
          <button className="btn small" onClick={openFolderPicker}>
            + Add folder
          </button>
          <button className="btn small" onClick={toggleFull} aria-pressed={full}>
            <ExpandIcon />
            {full ? "Exit full screen" : "Full screen"}
          </button>
          <button className="btn small" onClick={() => window.close()} title="Close this tab">
            Close
          </button>
        </div>
      </header>
      <div className="explore-body">{view === "tiles" ? <Treemap sel={sel} big /> : <TreeCanvas sel={sel} big />}</div>
      {overlays}
    </div>
  );
}
