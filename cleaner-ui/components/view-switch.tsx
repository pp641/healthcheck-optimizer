"use client";

import { tokenForNewTab } from "@/lib/api";

export type View = "canvas" | "tiles" | "list";
export const VIEW_KEY = "tidyfleet-disk-view";

export const views: { id: View; label: string; icon: React.ReactNode }[] = [
  {
    id: "canvas",
    label: "Tree",
    icon: (
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
        <rect x="1" y="6" width="4" height="4" rx="1" />
        <rect x="11" y="1.5" width="4" height="4" rx="1" />
        <rect x="11" y="10.5" width="4" height="4" rx="1" />
        <path d="M5 8c3 0 3-4.5 6-4.5M5 8c3 0 3 4.5 6 4.5" />
      </svg>
    ),
  },
  {
    id: "tiles",
    label: "Tiles",
    icon: (
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
        <rect x="1" y="1" width="8" height="14" rx="1" />
        <rect x="10" y="1" width="5" height="8" rx="1" />
        <rect x="10" y="10" width="5" height="5" rx="1" />
      </svg>
    ),
  },
  {
    id: "list",
    label: "List",
    icon: (
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden>
        <path d="M2 3.5h12M4 8h10M4 12.5h10" />
      </svg>
    ),
  },
];

export function ViewSwitch({ view, onPick, only }: { view: View; onPick: (v: View) => void; only?: View[] }) {
  return (
    <div className="view-switch" role="group" aria-label="View">
      {views
        .filter((v) => !only || only.includes(v.id))
        .map((v) => (
          <button key={v.id} aria-pressed={view === v.id} onClick={() => onPick(v.id)}>
            {v.icon}
            {v.label}
          </button>
        ))}
    </div>
  );
}

export function ExpandIcon() {
  return (
    <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M9.5 2.5h4v4M13.5 2.5L9 7M6.5 13.5h-4v-4M2.5 13.5L7 9" />
    </svg>
  );
}

/** Opens the Tree or Tiles view in its own tab, filling the whole window. */
export function openBigView(view: View) {
  const v = view === "list" ? "canvas" : view;
  window.open(`/explore/?view=${v}#token=${tokenForNewTab()}`, "_blank");
}
