"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";

import { useApp } from "@/components/app-context";
import { api, errorText } from "@/lib/api";
import { bytes, tilde } from "@/lib/format";
import type { Entry, Item, Pick } from "@/lib/types";

export type Selection = {
  selected: Map<string, Pick>;
  toggle: (p: Pick) => void;
  /** Selects or unselects any folder; a safe rule match is selected as a cleanup item. */
  toggleEntry: (e: Entry) => void;
  has: (e: Entry) => boolean;
  /** What a drag of e carries: the whole selection if e is part of it. */
  dragPaths: (e: Entry) => string[];
  /** Opens the confirm dialog for moving folders to the Trash or elsewhere. */
  manage: (action: "trash" | "move", paths: string[], dest?: string) => void;
};

export const DRAG_TYPE = "application/x-tidyfleet-paths";

/** True if dest is one of paths or inside one of them. */
export function insideAny(dest: string, paths: string[]) {
  return paths.some((p) => dest === p || dest.startsWith(p + "/"));
}

/** Drop handlers for a folder that accepts dragged folders. */
export function dropProps(dest: string | undefined, sel: Selection, setOver: (b: boolean) => void) {
  if (!dest) return {};
  return {
    onDragOver: (ev: React.DragEvent) => {
      if (!ev.dataTransfer.types.includes(DRAG_TYPE)) return;
      ev.preventDefault();
      ev.stopPropagation(); // nested drop targets (tiles) must not all react
      ev.dataTransfer.dropEffect = "move";
      setOver(true);
    },
    onDragLeave: () => setOver(false),
    onDrop: (ev: React.DragEvent) => {
      ev.preventDefault();
      ev.stopPropagation();
      setOver(false);
      const paths = JSON.parse(ev.dataTransfer.getData(DRAG_TYPE) || "[]") as string[];
      if (paths.length > 0 && !insideAny(dest, paths)) sel.manage("move", paths, dest);
    },
  };
}

const PAGE = 100;

/** Actions shared by folder rows and cache rows. */
export function RowMenu({ path, canRescan, onChanged }: { path: string; canRescan: boolean; onChanged: () => void }) {
  const { toast, startScan, state } = useApp();
  const [open, setOpen] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ top: 0, left: 0 });

  useEffect(() => {
    if (!open) return;
    const close = (e: Event) => {
      if (e instanceof KeyboardEvent && e.key !== "Escape") return;
      if (e instanceof MouseEvent && (menu.current?.contains(e.target as Node) || btn.current?.contains(e.target as Node))) return;
      setOpen(false);
    };
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", close);
    window.addEventListener("scroll", close, true);
    menu.current?.querySelector("button")?.focus();
    return () => {
      document.removeEventListener("mousedown", close);
      document.removeEventListener("keydown", close);
      window.removeEventListener("scroll", close, true);
    };
  }, [open]);

  const reveal = state?.platform === "darwin" ? "Show in Finder" : state?.platform === "windows" ? "Show in Explorer" : "Open folder";

  async function run(fn: () => Promise<void>) {
    setOpen(false);
    try {
      await fn();
    } catch (e) {
      toast(errorText(e));
    }
  }

  return (
    <>
      <button
        ref={btn}
        className="more-btn"
        aria-label={`Actions for ${path}`}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => {
          const r = btn.current!.getBoundingClientRect();
          setPos({ top: r.bottom + 4, left: Math.max(8, r.right - 200) });
          setOpen((o) => !o);
        }}
      >
        ⋯
      </button>
      {open && (
        <div ref={menu} className="menu" role="menu" style={{ top: pos.top, left: pos.left }}>
          <button role="menuitem" onClick={() => run(() => api("/api/reveal", { path }))}>
            {reveal}
          </button>
          <button
            role="menuitem"
            onClick={() =>
              run(async () => {
                await navigator.clipboard.writeText(path);
                toast("Path copied");
              })
            }
          >
            Copy path
          </button>
          {canRescan && (
            <button role="menuitem" onClick={() => run(() => startScan(path))}>
              Rescan this folder
            </button>
          )}
          <hr />
          <button
            role="menuitem"
            onClick={() =>
              run(async () => {
                if (!confirm(`Exclude ${tilde(path, state?.home ?? null)}?\n\nTidyfleet will never scan or clean it. You can undo this in Settings.`)) return;
                await api("/api/exclude", { path });
                toast("Excluded from scans");
                onChanged();
              })
            }
          >
            Exclude from scans
          </button>
        </div>
      )}
    </>
  );
}

function Chevron() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden>
      <path d="M4.5 2.5L8 6l-3.5 3.5" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function LockIcon() {
  return (
    <svg className="ico" viewBox="0 0 12 12" aria-hidden>
      <rect x="2.5" y="5.5" width="7" height="5" rx="1" fill="none" stroke="currentColor" strokeWidth="1.2" />
      <path d="M4 5.5V4a2 2 0 014 0v1.5" fill="none" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

/** short drops the rule name, for rows that already show it. */
export function ItemBadge({ item, short }: { item: Item; short?: boolean }) {
  const name = short ? "" : `${item.rule_name} · `;
  if (item.eligible) {
    const idle = item.kind === "project" && item.stale_days > 0 ? `idle ${item.stale_days} days · ` : "";
    return (
      <span className="badge safe" title={`${item.action}. Frees about ${bytes(item.size)}.`}>
        <span className="dot" aria-hidden />
        {name}
        {idle}safe to clean
      </span>
    );
  }
  return (
    <span className="badge skip" title={`Kept: ${item.skip_reason}`}>
      <LockIcon />
      {name}kept: {item.skip_reason}
    </span>
  );
}

function Row({ e, depth, max, sel, onChanged }: { e: Entry; depth: number; max: number; sel: Selection; onChanged: () => void }) {
  const { state } = useApp();
  const [open, setOpen] = useState(false);
  const [over, setOver] = useState(false);
  const expandable = e.kind === "dir" && e.has_children && !e.collapsed && !e.locked;
  const item = e.item;
  const selectable = e.kind === "dir" && !!e.path && !e.locked && depth > 0;
  const checked = selectable && sel.has(e);
  const name = depth === 0 ? tilde(e.path, state?.home ?? null) : e.kind === "files" ? `${e.files.toLocaleString()} ${e.files === 1 ? "file" : "files"} directly in this folder` : e.name;

  return (
    <>
      <div
        className={`row${checked ? " selected" : ""}${over ? " drop-over" : ""}`}
        draggable={selectable}
        onDragStart={(ev) => {
          ev.dataTransfer.setData(DRAG_TYPE, JSON.stringify(sel.dragPaths(e)));
          ev.dataTransfer.effectAllowed = "move";
        }}
        {...(e.kind === "dir" && !e.locked ? dropProps(e.path, sel, setOver) : {})}
        role="treeitem"
        aria-expanded={expandable ? open : undefined}
        aria-level={depth + 1}
        style={{ ["--depth" as string]: depth }}
      >
        {expandable ? (
          <button className="chev" aria-label={open ? `Collapse ${e.name}` : `Expand ${e.name}`} aria-expanded={open} onClick={() => setOpen(!open)}>
            <Chevron />
          </button>
        ) : (
          <span />
        )}
        {selectable ? (
          <input
            type="checkbox"
            checked={checked}
            className={item?.eligible ? undefined : "folder-check"}
            aria-label={item?.eligible ? `Select ${e.name} for cleanup` : `Select folder ${e.name}`}
            onChange={() => sel.toggleEntry(e)}
          />
        ) : (
          <span />
        )}
        <span className="name">
          <span
            className={`name-text${expandable ? " dir" : ""}${e.kind === "files" ? " loose" : ""}${depth === 0 ? " root-path" : ""}`}
            title={e.path}
            onClick={expandable ? () => setOpen(!open) : undefined}
          >
            {name}
          </span>
          {item && <ItemBadge item={item} />}
          {!item && e.collapsed && <span className="badge">not expanded</span>}
          {e.locked && (
            <span className="badge">
              <LockIcon />
              excluded
            </span>
          )}
          {e.denied && <span className="badge">no access</span>}
        </span>
        <span className="bar" aria-hidden>
          <span className="bar-fill" style={{ display: "block", width: `${max > 0 ? Math.max(0.5, (e.size / max) * 100) : 0}%` }} />
        </span>
        <span className="size">{e.locked ? "—" : bytes(e.size)}</span>
        {e.kind === "dir" && e.path ? <RowMenu path={e.path} canRescan={!e.locked} onChanged={onChanged} /> : <span />}
      </div>
      {open && expandable && e.path && <Level path={e.path} depth={depth + 1} parentSize={e.size} sel={sel} />}
    </>
  );
}

function Level({ path, depth, parentSize, sel }: { path: string; depth: number; parentSize: number; sel: Selection }) {
  const { treeVersion, bumpTree } = useApp();
  const [entries, setEntries] = useState<Entry[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [limit, setLimit] = useState(PAGE);

  useEffect(() => {
    let live = true;
    api<{ entries: Entry[] }>(`/api/tree?path=${encodeURIComponent(path)}`)
      .then((r) => live && setEntries(r.entries))
      .catch((e) => live && setErr(errorText(e)));
    return () => {
      live = false;
    };
  }, [path, treeVersion]);

  if (err) return <p className="show-more msg error" style={{ ["--depth" as string]: depth }}>{err}</p>;
  if (!entries) return <p className="show-more subtle" style={{ ["--depth" as string]: depth }}>Loading…</p>;
  if (entries.length === 0) return <p className="show-more subtle" style={{ ["--depth" as string]: depth }}>Empty folder</p>;
  return (
    <div role="group">
      {entries.slice(0, limit).map((e) => (
        <Row key={e.path ?? e.name} e={e} depth={depth} max={parentSize} sel={sel} onChanged={bumpTree} />
      ))}
      {entries.length > limit && (
        <div className="show-more" style={{ ["--depth" as string]: depth }}>
          <button className="btn link" onClick={() => setLimit(limit + PAGE)}>
            Show {Math.min(PAGE, entries.length - limit)} more of {entries.length - limit} smaller items
          </button>
        </div>
      )}
    </div>
  );
}

export function DiskTree({ sel }: { sel: Selection }) {
  const { state, bumpTree } = useApp();
  const roots = state?.roots;
  if (!roots) return <div className="empty">{state?.scanning ? "Measuring your folders…" : "No scan yet."}</div>;
  if (roots.length === 0) {
    return (
      <div className="empty">
        No project folders to scan yet. Add the folders where you keep code in <Link href="/settings/">Settings</Link>.
      </div>
    );
  }
  const max = Math.max(...roots.map((r) => r.size), 1);
  return (
    <>
      {roots.map((r) => (
        <Row key={r.path} e={r} depth={0} max={max} sel={sel} onChanged={bumpTree} />
      ))}
    </>
  );
}
