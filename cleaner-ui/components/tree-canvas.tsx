"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { useApp } from "@/components/app-context";
import { Inspector } from "@/components/inspector";
import { insideAny, type Selection } from "@/components/tree";
import { bytes } from "@/lib/format";
import { colorFor, useDark } from "@/lib/ramp";
import { isExpandable, nameOf, ROOT, useTreeData } from "@/lib/tree-data";
import type { Entry } from "@/lib/types";

const NODE_W = 216;
const NODE_H = 38;
const COL = 290; // horizontal distance between levels
const ROW = 48; // vertical distance between leaves
const MAX_KIDS = 12; // more are folded into one "+N smaller" node

type VNode = {
  key: string;
  entry: Entry;
  depth: number;
  children: VNode[];
  ghost: boolean; // hover preview, not yet opened
  expanded: boolean;
  x: number;
  y: number;
};

/** The folder itself plus every folder between it and its scan location. */
function chain(path: string, roots: Entry[]): string[] {
  const root = roots.find((r) => r.path && (path === r.path || path.startsWith(r.path + "/") || path.startsWith(r.path + "\\")));
  if (!root?.path) return [path];
  const out = [path];
  let p = path;
  while (p.length > root.path.length) {
    const i = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\"));
    if (i <= 0) break;
    p = p.slice(0, i);
    out.push(p);
  }
  return out;
}

function fit(name: string, px: number, keepEnd = false): string {
  const max = Math.floor(px / 7.4);
  if (name.length <= max) return name;
  return keepEnd ? "…" + name.slice(name.length - max + 1) : name.slice(0, Math.max(1, max - 1)) + "…";
}

export function TreeCanvas({ sel, big }: { sel: Selection; big?: boolean }) {
  const { state } = useApp();
  const { get, load } = useTreeData();
  const dark = useDark();
  const [expanded, setExpanded] = useState<Set<string>>(new Set([ROOT]));
  const [hover, setHover] = useState<string | null>(null);
  const [picked, setPicked] = useState<Entry | null>(null);
  const [view, setView] = useState({ x: 40, y: 40, k: 1 });
  const [glide, setGlide] = useState(false); // animate button/open moves, not drag or wheel
  const svgRef = useRef<SVGSVGElement>(null);
  // Dragging a node onto another folder moves it there.
  const [dragging, setDragging] = useState<{ paths: string[]; label: string; x: number; y: number; target: string | null } | null>(null);
  const suppressClick = useRef(false);
  const dragActive = useRef(false); // freezes hover previews so the layout doesn't shift mid-drag
  const drag = useRef<{ x: number; y: number; vx: number; vy: number; moved: boolean } | null>(null);
  const fitted = useRef(false);

  const roots = useMemo(() => state?.roots ?? [], [state?.roots]);
  const total = roots.reduce((a, r) => a + r.size, 0);

  // Open every scan location to start with.
  useEffect(() => {
    if (roots.length === 0) return;
    Promise.all(roots.filter(isExpandable).map((r) => load(r.path!))).then(() =>
      setExpanded((prev) => new Set([...prev, ...roots.filter(isExpandable).map((r) => r.path!)])),
    );
  }, [roots, load]);

  const tree = useMemo(() => {
    const rootEntry: Entry = { name: "Scan locations", kind: "dir", size: total, files: roots.reduce((a, r) => a + r.files, 0), has_children: true };
    const make = (entry: Entry, key: string, depth: number, ghost: boolean): VNode => {
      const n: VNode = { key, entry, depth, children: [], ghost, expanded: false, x: depth * COL, y: 0 };
      const path = key === ROOT ? ROOT : entry.path;
      if (path === undefined || (key !== ROOT && !isExpandable(entry))) return n;
      const open = expanded.has(path);
      const preview = !open && !ghost && hover === path;
      if (!open && !preview) return n;
      const kids = get(path);
      if (!kids) return n;
      n.expanded = open;
      // Empty folders stay visible (you can drop things into them); empty loose-file rows don't.
      const shown = kids.filter((k) => k.size > 0 || k.kind === "dir").slice(0, MAX_KIDS);
      for (const k of shown) {
        n.children.push(make(k, k.path ?? `${key}#files`, depth + 1, ghost || preview));
      }
      const rest = kids.filter((k) => k.size > 0 || k.kind === "dir").slice(MAX_KIDS);
      if (rest.length > 0) {
        const more: Entry = {
          name: `+${rest.length} smaller`,
          kind: "files",
          size: rest.reduce((a, k) => a + k.size, 0),
          files: rest.reduce((a, k) => a + k.files, 0),
          has_children: false,
        };
        n.children.push(make(more, `${key}#more`, depth + 1, ghost || preview));
      }
      return n;
    };
    const root = make(rootEntry, ROOT, 0, false);
    // Tidy layout: leaves get rows, parents sit centered on their children.
    let row = 0;
    const place = (n: VNode) => {
      if (n.children.length === 0) {
        n.y = row++ * ROW;
      } else {
        n.children.forEach(place);
        n.y = (n.children[0].y + n.children[n.children.length - 1].y) / 2;
      }
    };
    place(root);
    const all: VNode[] = [];
    const walk = (n: VNode) => {
      all.push(n);
      n.children.forEach(walk);
    };
    walk(root);
    return { root, all };
  }, [expanded, hover, get, roots, total]);

  const fitView = useCallback(() => {
    const svg = svgRef.current;
    if (!svg || tree.all.length === 0) return;
    const { width, height } = svg.getBoundingClientRect();
    const xs = tree.all.map((n) => n.x);
    const ys = tree.all.map((n) => n.y);
    const minX = Math.min(...xs) - 20;
    const maxX = Math.max(...xs) + NODE_W + 40 + COL; // room for one more level of previews
    const minY = Math.min(...ys) - 20;
    const maxY = Math.max(...ys) + NODE_H + 20;
    const k = Math.max(0.3, Math.min(1.2, Math.min(width / (maxX - minX), height / (maxY - minY))));
    setGlide(true);
    setView({ k, x: (width - (maxX - minX) * k) / 2 - minX * k, y: (height - (maxY - minY) * k) / 2 - minY * k });
  }, [tree]);

  // Fit once when the first scan location opens.
  useEffect(() => {
    if (!fitted.current && tree.all.length > roots.length + 1) {
      fitted.current = true;
      fitView();
    }
  }, [tree, roots.length, fitView]);

  // Wheel zooms around the cursor (non-passive so the page does not scroll).
  useEffect(() => {
    const svg = svgRef.current;
    if (!svg) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = svg.getBoundingClientRect();
      const cx = e.clientX - r.left;
      const cy = e.clientY - r.top;
      setGlide(false);
      setView((v) => {
        const k = Math.max(0.25, Math.min(2.5, v.k * Math.exp(-e.deltaY * 0.0015)));
        return { k, x: cx - ((cx - v.x) * k) / v.k, y: cy - ((cy - v.y) * k) / v.k };
      });
    };
    svg.addEventListener("wheel", onWheel, { passive: false });
    return () => svg.removeEventListener("wheel", onWheel);
  }, []);

  function zoom(f: number) {
    const svg = svgRef.current;
    if (!svg) return;
    const { width, height } = svg.getBoundingClientRect();
    setGlide(true);
    setView((v) => {
      const k = Math.max(0.25, Math.min(2.5, v.k * f));
      return { k, x: width / 2 - ((width / 2 - v.x) * k) / v.k, y: height / 2 - ((height / 2 - v.y) * k) / v.k };
    });
  }

  function startNodeDrag(ev: React.PointerEvent, n: VNode) {
    const e = n.entry;
    if (ev.button !== 0 || n.key === ROOT || n.ghost || !e.path || e.locked || n.depth === 1) return;
    ev.stopPropagation();
    const x0 = ev.clientX;
    const y0 = ev.clientY;
    let paths: string[] | null = null;
    const targetAt = (x: number, y: number, carried: string[]) => {
      const el = document.elementFromPoint(x, y)?.closest("g.node[data-drop]") as SVGGElement | null;
      const t = el?.getAttribute("data-drop") ?? null;
      return t && !insideAny(t, carried) && !carried.some((c) => c.slice(0, c.lastIndexOf("/")) === t) ? t : null;
    };
    const move = (m: PointerEvent) => {
      if (!paths) {
        if (Math.hypot(m.clientX - x0, m.clientY - y0) < 6) return;
        paths = sel.dragPaths(e);
        dragActive.current = true;
      }
      const label = paths.length === 1 ? (e.name ?? "") : `${paths.length} folders`;
      setDragging({ paths, label, x: m.clientX, y: m.clientY, target: targetAt(m.clientX, m.clientY, paths) });
    };
    const up = (u: PointerEvent) => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      if (!paths) return;
      dragActive.current = false;
      suppressClick.current = true;
      const target = targetAt(u.clientX, u.clientY, paths);
      setDragging(null);
      if (target) sel.manage("move", paths, target);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  }

  function onNodeClick(ev: React.MouseEvent, n: VNode) {
    if (suppressClick.current) {
      suppressClick.current = false;
      return;
    }
    const e = n.entry;
    if ((ev.metaKey || ev.ctrlKey || ev.shiftKey) && e.path && e.kind === "dir" && !e.locked && n.depth > 1) {
      sel.toggleEntry(e);
      setPicked(e);
      return;
    }
    activate(n);
  }

  function activate(n: VNode) {
    if (n.key === ROOT) return;
    setPicked(n.entry);
    const path = n.entry.path;
    if (n.ghost) {
      // Clicking a previewed branch keeps it: open the whole chain down to it.
      openChain(path && isExpandable(n.entry) ? path : hover);
      return;
    }
    if (!path || !isExpandable(n.entry)) return;
    const opening = !expanded.has(path);
    load(path).then(() => {
      setExpanded((prev) => {
        const next = new Set(prev);
        if (next.has(path)) next.delete(path);
        else next.add(path);
        return next;
      });
      if (opening) bringIntoView(n);
    });
  }

  // Slide the view so an opened folder's new branches are on screen.
  function bringIntoView(n: VNode) {
    const svg = svgRef.current;
    if (!svg) return;
    const { width, height } = svg.getBoundingClientRect();
    setGlide(true);
    setView((v) => {
      const right = (n.x + COL + NODE_W) * v.k + v.x;
      const nodeY = (n.y + NODE_H / 2) * v.k + v.y;
      let x = v.x;
      if (right > width - 20) x -= right - (width - 20);
      if (n.x * v.k + x < 20) x = 20 - n.x * v.k;
      const y = nodeY < 60 || nodeY > height - 60 ? v.y + (height / 2 - nodeY) : v.y;
      return { ...v, x, y };
    });
  }

  function openChain(path: string | null | undefined) {
    if (!path) return;
    const paths = chain(path, roots);
    Promise.all(paths.map((p) => load(p))).then(() => setExpanded((prev) => new Set([...prev, ...paths])));
    setHover(null);
  }

  function onHover(n: VNode) {
    if (n.ghost || dragActive.current) return; // keep the parent's preview while moving across it
    const path = n.entry.path;
    if (path && isExpandable(n.entry) && !expanded.has(path)) {
      setHover(path);
      load(path);
    } else if (n.key !== hover) {
      setHover(null);
    }
  }

  if (roots.length === 0) return <div className="empty card">{state?.scanning ? "Measuring your folders…" : "No project folders yet. Add some in Settings."}</div>;

  return (
    <div className="viz-layout">
      <div className={`canvas-wrap card${big ? " big" : ""}`}>
        <div className="canvas-tools" role="toolbar" aria-label="Canvas controls">
          <button className="btn small" onClick={() => zoom(1.25)} aria-label="Zoom in">
            +
          </button>
          <button className="btn small" onClick={() => zoom(0.8)} aria-label="Zoom out">
            −
          </button>
          <button className="btn small" onClick={fitView}>
            Fit
          </button>
          <button
            className="btn small"
            onClick={() => {
              setExpanded(new Set([ROOT, ...roots.filter(isExpandable).map((r) => r.path!)]));
              setPicked(null);
              fitted.current = false;
            }}
          >
            Collapse
          </button>
        </div>
        <p className="canvas-hint subtle">Drag to move · scroll to zoom · hover a folder to preview its branches · click to open</p>
        <svg
          ref={svgRef}
          className="canvas"
          role="tree"
          aria-label="Folder tree canvas"
          onPointerDown={(e) => {
            if ((e.target as Element).closest(".node")) return;
            drag.current = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y, moved: false };
            setGlide(false);
            (e.currentTarget as Element).setPointerCapture(e.pointerId);
          }}
          onPointerMove={(e) => {
            const d = drag.current;
            if (!d) return;
            d.moved = true;
            setView((v) => ({ ...v, x: d.vx + e.clientX - d.x, y: d.vy + e.clientY - d.y }));
          }}
          onPointerUp={() => {
            if (drag.current && !drag.current.moved) setHover(null);
            drag.current = null;
          }}
          onPointerLeave={() => !dragActive.current && setHover(null)}
        >
          <g className={glide ? "glide" : undefined} style={{ transform: `translate(${view.x}px,${view.y}px) scale(${view.k})`, transformOrigin: "0 0" }}>
            {tree.all.map((n) =>
              n.children.map((c) => {
                const share = n.entry.size > 0 ? c.entry.size / n.entry.size : 0;
                const x1 = n.x + NODE_W;
                const y1 = n.y + NODE_H / 2;
                const x2 = c.x;
                const y2 = c.y + NODE_H / 2;
                const mx = (x1 + x2) / 2;
                return (
                  <path
                    key={`${n.key}>${c.key}`}
                    className={`branch${c.ghost ? " ghost" : ""}`}
                    d={`M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}`}
                    strokeWidth={1.5 + 14 * share}
                  />
                );
              }),
            )}
            {tree.all.map((n) => {
              const e = n.entry;
              const isRoot = n.key === ROOT;
              const item = e.item;
              const checked = item ? sel.selected.has(item.id) : false;
              const expandable = isRoot ? false : isExpandable(e);
              const isPicked = picked && picked.path && picked.path === e.path;
              const parentShare = total > 0 ? e.size / total : 0;
              const label = isRoot ? e.name : nameOf(e, state?.scan_roots, state?.home);
              return (
                <g
                  key={n.key}
                  className={`node${n.ghost ? " ghost" : ""}${isPicked ? " picked" : ""}${isRoot ? " root" : ""}${
                    !isRoot && sel.has(e) ? " selected" : ""
                  }${dragging && dragging.target === e.path ? " drop" : ""}${dragging && e.path && dragging.paths.includes(e.path) ? " dragging" : ""}`}
                  data-drop={!isRoot && !n.ghost && e.kind === "dir" && e.path && !e.locked ? e.path : undefined}
                  onPointerDown={(ev) => startNodeDrag(ev, n)}
                  transform={`translate(${n.x},${n.y})`}
                  role="treeitem"
                  aria-level={n.depth + 1}
                  aria-expanded={expandable ? n.expanded : undefined}
                  aria-label={`${label}, ${bytes(e.size)}${item ? (item.eligible ? ", safe to clean" : ", kept for safety") : ""}`}
                  tabIndex={isRoot ? -1 : 0}
                  onMouseEnter={() => onHover(n)}
                  onClick={(ev) => onNodeClick(ev, n)}
                  onKeyDown={(ev) => {
                    if (ev.key === "Enter" || ev.key === " ") {
                      ev.preventDefault();
                      activate(n);
                    }
                  }}
                >
                  <title>{e.path ?? label}</title>
                  <rect className="node-box" width={NODE_W} height={NODE_H} rx={9} />
                  {!isRoot && <rect x={0} y={0} width={6} height={NODE_H} rx={3} fill={colorFor(parentShare, dark)} />}
                  <text className="node-name" x={isRoot ? 12 : 16} y={16}>
                    {fit(label, NODE_W - (item ? 58 : 36), n.depth === 1)}
                  </text>
                  <text className="node-size" x={isRoot ? 12 : 16} y={30}>
                    {e.locked ? "excluded" : bytes(e.size)}
                    {!isRoot && total > 0 && !e.locked ? ` · ${((e.size / total) * 100).toFixed(e.size / total < 0.1 ? 1 : 0)}%` : ""}
                    {e.collapsed && !item ? " · not expanded" : ""}
                  </text>
                  {item?.eligible && (
                    <g
                      className="node-check"
                      transform={`translate(${NODE_W - 22},${NODE_H / 2})`}
                      onClick={(ev) => {
                        ev.stopPropagation();
                        sel.toggle({ id: item.id, name: e.path ?? e.name, size: item.size });
                      }}
                      role="checkbox"
                      aria-checked={checked}
                      aria-label={`Select ${e.name} for cleanup`}
                    >
                      <circle r={11} className="hit" />
                      <circle r={8} className={checked ? "check-on" : "check-off"} />
                      {checked && <path d="M-3.5,0 L-1,2.6 L3.8,-2.6" className="check-mark" />}
                    </g>
                  )}
                  {item && !item.eligible && (
                    <g transform={`translate(${NODE_W - 24},${NODE_H / 2 - 7})`} className="node-lock">
                      <rect x={1.5} y={6} width={11} height={8} rx={1.5} />
                      <path d="M4,6 V4 a3,3 0 0 1 6,0 V6" />
                    </g>
                  )}
                  {expandable && !n.ghost && (
                    <g transform={`translate(${NODE_W},${NODE_H / 2})`} className="node-toggle">
                      <circle r={9} />
                      <path d={n.expanded ? "M-4,0 H4" : "M-4,0 H4 M0,-4 V4"} />
                    </g>
                  )}
                </g>
              );
            })}
          </g>
        </svg>
      </div>
      {dragging && (
        <div className="drag-ghost" style={{ left: dragging.x, top: dragging.y }}>
          {dragging.target ? `Move ${dragging.label} here` : `Moving ${dragging.label}…`}
        </div>
      )}
      <Inspector
        entry={picked ?? (hover ? tree.all.find((n) => n.entry.path === hover)?.entry ?? null : null)}
        total={total}
        sel={sel}
        onOpen={(e) => openChain(e.path)}
      />
    </div>
  );
}
