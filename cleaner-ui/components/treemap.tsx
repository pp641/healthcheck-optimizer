"use client";

import { useEffect, useMemo, useRef, useState } from "react";

import { useApp } from "@/components/app-context";
import { Inspector } from "@/components/inspector";
import { DRAG_TYPE, dropProps, type Selection } from "@/components/tree";
import { bytes, tilde } from "@/lib/format";
import { colorFor, inkOn, ramp, useDark } from "@/lib/ramp";
import { isExpandable, nameOf, ROOT, useTreeData } from "@/lib/tree-data";
import type { Entry } from "@/lib/types";

type Rect = { x: number; y: number; w: number; h: number };
type Tile = Rect & { e: Entry };

const HEIGHT = 560;
const HEADER = 20; // label strip on tiles that show their sub-folders
const NEST_MIN = { w: 150, h: 96 }; // tiles at least this big show sub-folders

/** Squarified treemap (Bruls et al.): keeps tiles close to square. */
function squarify(items: Entry[], box: Rect): Tile[] {
  const list = items.filter((e) => e.size > 0).sort((a, b) => b.size - a.size);
  const sum = list.reduce((a, e) => a + e.size, 0);
  if (sum === 0 || box.w <= 0 || box.h <= 0) return [];
  const scale = (box.w * box.h) / sum;
  const out: Tile[] = [];
  let rect = { ...box };
  let row: Entry[] = [];
  const worst = (r: Entry[], side: number) => {
    const areas = r.map((e) => e.size * scale);
    const s = areas.reduce((a, b) => a + b, 0);
    return Math.max(...areas.map((a) => Math.max((side * side * a) / (s * s), (s * s) / (side * side * a))));
  };
  const flush = () => {
    const areas = row.map((e) => e.size * scale);
    const s = areas.reduce((a, b) => a + b, 0);
    if (rect.w >= rect.h) {
      const w = s / rect.h;
      let y = rect.y;
      row.forEach((e, i) => {
        const h = areas[i] / w;
        out.push({ e, x: rect.x, y, w, h });
        y += h;
      });
      rect = { x: rect.x + w, y: rect.y, w: rect.w - w, h: rect.h };
    } else {
      const h = s / rect.w;
      let x = rect.x;
      row.forEach((e, i) => {
        const w = areas[i] / h;
        out.push({ e, x, y: rect.y, w, h });
        x += w;
      });
      rect = { x: rect.x, y: rect.y + h, w: rect.w, h: rect.h - h };
    }
    row = [];
  };
  for (const e of list) {
    const side = Math.min(rect.w, rect.h);
    if (row.length === 0 || worst([...row, e], side) <= worst(row, side)) {
      row.push(e);
    } else {
      flush();
      row.push(e);
    }
  }
  if (row.length) flush();
  return out;
}

export function Treemap({ sel, big }: { sel: Selection; big?: boolean }) {
  const { state } = useApp();
  const { get, load } = useTreeData();
  const dark = useDark();
  const wrap = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(800);
  const [boxHeight, setBoxHeight] = useState(600);
  const [current, setCurrent] = useState<string>(ROOT);
  const [picked, setPicked] = useState<Entry | null>(null);
  const [tip, setTip] = useState<{ e: Entry; x: number; y: number } | null>(null);
  const [overPath, setOverPath] = useState<string | null>(null);
  const dropOn = (dest: string | undefined) => dropProps(dest, sel, (b) => setOverPath(b && dest ? dest : null));
  const label = (e: Entry) => nameOf(e, state?.scan_roots, state?.home);

  useEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(([entry]) => {
      setWidth(Math.floor(entry.contentRect.width));
      setBoxHeight(Math.floor(entry.contentRect.height));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const roots = useMemo(() => state?.roots ?? [], [state?.roots]);
  const entries = get(current);
  useEffect(() => {
    if (current !== ROOT) load(current).catch(() => setCurrent(ROOT));
  }, [current, load]);

  // In the big view the map fills the window; otherwise it has a fixed height.
  const height = big ? Math.max(300, boxHeight) : width < 600 ? 420 : HEIGHT;
  const tiles = useMemo(() => squarify(entries ?? [], { x: 0, y: 0, w: width, h: height }), [entries, width, height]);
  const levelTotal = tiles.reduce((a, t) => a + t.e.size, 0);
  const maxSize = tiles.length ? tiles[0].e.size : 1;

  // Fetch sub-folders for tiles big enough to show them.
  const nestable = tiles.filter((t) => t.w >= NEST_MIN.w && t.h >= NEST_MIN.h && isExpandable(t.e));
  useEffect(() => {
    nestable.forEach((t) => load(t.e.path!));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nestable.map((t) => t.e.path).join("|"), load]);

  const crumbs = useMemo(() => {
    const out: { name: string; path: string }[] = [{ name: "All locations", path: ROOT }];
    if (current === ROOT) return out;
    const root = roots.find((r) => r.path && (current === r.path || current.startsWith(r.path + "/")));
    if (!root?.path) return out;
    out.push({ name: tilde(root.path, state?.home ?? null), path: root.path });
    let acc = root.path;
    for (const part of current.slice(root.path.length).split("/").filter(Boolean)) {
      acc += "/" + part;
      out.push({ name: part, path: acc });
    }
    return out;
  }, [current, roots, state?.home]);

  function open(e: Entry) {
    setPicked(e);
    setTip(null);
    if (isExpandable(e)) setCurrent(e.path!);
  }

  function tileView(t: Tile, depth: number) {
    const e = t.e;
    const bg = e.locked ? "var(--surface-2)" : colorFor(e.size / maxSize, dark);
    const ink = e.locked ? "var(--muted)" : inkOn(bg);
    const item = e.item;
    const checked = item ? sel.selected.has(item.id) : false;
    const isRootTile = !!e.path && (state?.scan_roots ?? []).includes(e.path);
    const movable = e.kind === "dir" && !!e.path && !e.locked && !isRootTile;
    const selected = movable && !item?.eligible && sel.has(e);
    const nested = depth === 0 && t.w >= NEST_MIN.w && t.h >= NEST_MIN.h && isExpandable(e) ? get(e.path!) : undefined;
    const inner = nested ? squarify(nested, { x: 3, y: HEADER, w: t.w - 8, h: t.h - HEADER - 5 }) : [];
    const showText = t.w > 56 && t.h > 30;
    return (
      <div
        key={e.path ?? `files-${depth}-${t.x}-${t.y}`}
        className={`tile${checked ? " checked" : ""}${selected ? " selected" : ""}${picked?.path && picked.path === e.path ? " picked" : ""}${
          depth > 0 ? " inner" : ""
        }${overPath && overPath === e.path ? " drop-over" : ""}`}
        draggable={movable}
        onDragStart={(ev) => {
          ev.stopPropagation();
          setTip(null);
          ev.dataTransfer.setData(DRAG_TYPE, JSON.stringify(sel.dragPaths(e)));
          ev.dataTransfer.effectAllowed = "move";
        }}
        {...(e.kind === "dir" && !e.locked ? dropOn(e.path) : {})}
        style={{ left: t.x + 1, top: t.y + 1, width: Math.max(0, t.w - 2), height: Math.max(0, t.h - 2), background: bg, color: ink }}
        role="button"
        tabIndex={depth === 0 ? 0 : -1}
        aria-label={`${label(e)}, ${bytes(e.size)}${item ? (item.eligible ? ", safe to clean" : ", kept for safety") : ""}${isExpandable(e) ? ", open" : ""}`}
        onClick={(ev) => {
          ev.stopPropagation();
          if ((ev.metaKey || ev.ctrlKey || ev.shiftKey) && movable) {
            sel.toggleEntry(e);
            setPicked(e);
            return;
          }
          open(e);
        }}
        onKeyDown={(ev) => {
          if (ev.key === "Enter" || ev.key === " ") {
            ev.preventDefault();
            open(e);
          }
        }}
        onMouseMove={(ev) => {
          ev.stopPropagation();
          const r = wrap.current!.getBoundingClientRect();
          setTip({ e, x: ev.clientX - r.left, y: ev.clientY - r.top });
        }}
      >
        {showText && (
          <span className="tile-label">
            <span className="tile-name">{label(e)}</span>
            {(inner.length === 0 || t.h > 60) && <span className="tile-size">{e.locked ? "excluded" : bytes(e.size)}</span>}
          </span>
        )}
        {item && t.w > 26 && t.h > 20 && (
          <span className={`tile-mark ${item.eligible ? (checked ? "on" : "safe") : "kept"}`} aria-hidden>
            {item.eligible && checked ? "✓" : ""}
          </span>
        )}
        {inner.map((c) => tileView(c, depth + 1))}
      </div>
    );
  }

  if (roots.length === 0) return <div className="empty card">{state?.scanning ? "Measuring your folders…" : "No project folders yet. Add some in Settings."}</div>;

  const r = ramp(dark);
  return (
    <div className="viz-layout">
      <div className={`card treemap-card${big ? " big" : ""}`}>
        <div className="treemap-head">
          <nav className="crumbs" aria-label="Folder path">
            {crumbs.map((c, i) => (
              <span key={c.path}>
                {i > 0 && <span className="crumb-sep">/</span>}
                {i === crumbs.length - 1 ? (
                  <span className="crumb-current">{c.name}</span>
                ) : (
                  <button
                    className={`btn link${overPath === c.path ? " crumb-drop" : ""}`}
                    {...(c.path !== ROOT ? dropOn(c.path) : {})}
                    onClick={() => {
                      setCurrent(c.path);
                      setTip(null);
                    }}
                  >
                    {c.name}
                  </button>
                )}
              </span>
            ))}
          </nav>
          <div className="legend" aria-label="Color scale">
            <span className="subtle">smaller</span>
            <span className="legend-bar" style={{ background: `linear-gradient(90deg, ${r.join(",")})` }} />
            <span className="subtle">larger</span>
          </div>
        </div>
        <div ref={wrap} className={`treemap${big ? " big" : ""}`} style={big ? undefined : { height }} onMouseLeave={() => setTip(null)}>
          {!entries ? (
            <div className="empty">Loading…</div>
          ) : tiles.length === 0 ? (
            <div className="empty">This folder is empty.</div>
          ) : (
            tiles.map((t) => tileView(t, 0))
          )}
          {tip && (
            <div className="tile-tip" style={{ left: Math.min(tip.x + 14, width - 240), top: tip.y + 14 }}>
              <strong>{label(tip.e)}</strong>
              <div>
                {tip.e.locked ? "excluded" : bytes(tip.e.size)}
                {levelTotal > 0 && !tip.e.locked && ` · ${((tip.e.size / levelTotal) * 100).toFixed(1)}% of this view`}
              </div>
              {tip.e.item && <div>{tip.e.item.eligible ? `${tip.e.item.rule_name}: safe to clean` : `Kept: ${tip.e.item.skip_reason}`}</div>}
              {isExpandable(tip.e) && <div className="subtle">Click to open</div>}
            </div>
          )}
        </div>
        <p className="subtle treemap-foot">Area and color both show size. Tiles with a dot are safe to clean; a lock means a rule matched but the folder is kept.</p>
      </div>
      <Inspector entry={picked} total={levelTotal} sel={sel} onOpen={open} />
    </div>
  );
}
