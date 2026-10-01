"use client";

import { useId, useMemo, useRef, useState } from "react";

export type Datum = { t: number; v: number };

type Props = {
  title: string;
  data: Datum[];
  unit: "pct" | "gb";
  /** Fixed y-domain, e.g. [0, 100] for percentages. Otherwise 0..nice(max). */
  domain?: [number, number];
  /** Optional alert threshold, drawn as a hairline reference. */
  threshold?: { value: number; label: string };
};

const W = 520;
const H = 180;
const M = { top: 12, right: 14, bottom: 22, left: 40 };

/** A clean tick step (1, 2 or 5 × 10^n) giving about four intervals up to max. */
function niceStep(max: number): number {
  const raw = Math.max(max, 1e-9) / 4;
  const exp = Math.pow(10, Math.floor(Math.log10(raw)));
  for (const m of [1, 2, 5, 10]) if (m * exp >= raw) return m * exp;
  return 10 * exp;
}

const dayFmt = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short" });
const hourFmt = new Intl.DateTimeFormat("en-GB", { hour: "2-digit", minute: "2-digit" });
const fullFmt = new Intl.DateTimeFormat("en-GB", { dateStyle: "medium", timeStyle: "short" });

/**
 * One series over time: 2px line, 10% area wash, hairline grid, end dot with a
 * surface ring, crosshair tooltip on hover. Single series, so no legend: the
 * title names it.
 */
const formats = {
  pct: (v: number) => `${Math.round(v)}%`,
  gb: (v: number) => `${Number.isInteger(v) || v >= 10 ? Math.round(v) : v.toFixed(1)} GB`,
};

export function LineChart({ title, data, unit, domain, threshold }: Props) {
  const format = formats[unit];
  const ref = useRef<SVGSVGElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const clipId = useId();

  const geo = useMemo(() => {
    if (data.length === 0) return null;
    const t0 = data[0].t;
    const t1 = Math.max(data[data.length - 1].t, t0 + 1);
    const vmax = Math.max(...data.map((d) => d.v), threshold?.value ?? 0);
    const step = domain ? (domain[1] - domain[0]) / 4 : niceStep(vmax * 1.05);
    const [y0, y1] = domain ?? [0, Math.max(step, Math.ceil((vmax * 1.05) / step) * step)];
    const x = (t: number) => M.left + ((t - t0) / (t1 - t0)) * (W - M.left - M.right);
    const y = (v: number) => H - M.bottom - ((v - y0) / (y1 - y0 || 1)) * (H - M.top - M.bottom);
    const yTicks: number[] = [];
    for (let v = y0; v <= y1 + step / 2; v += step) yTicks.push(v);
    const span = t1 - t0;
    const nx = 4;
    const xTicks = Array.from({ length: nx + 1 }, (_, i) => t0 + (span * i) / nx);
    const line = data.map((d, i) => `${i ? "L" : "M"}${x(d.t).toFixed(1)},${y(d.v).toFixed(1)}`).join("");
    const area = `${line}L${x(data[data.length - 1].t).toFixed(1)},${y(y0)}L${x(t0).toFixed(1)},${y(y0)}Z`;
    return { x, y, yTicks, xTicks, line, area, short: span < 2 * 86400000, y0, y1 };
  }, [data, domain, threshold]);

  const last = data[data.length - 1];

  function onMove(e: React.PointerEvent<SVGSVGElement>) {
    if (!geo || !ref.current) return;
    const rect = ref.current.getBoundingClientRect();
    const px = ((e.clientX - rect.left) / rect.width) * W;
    let best = 0;
    for (let i = 1; i < data.length; i++) {
      if (Math.abs(geo.x(data[i].t) - px) < Math.abs(geo.x(data[best].t) - px)) best = i;
    }
    setHover(best);
  }

  const h = hover != null ? data[hover] : null;

  return (
    <div className="card chart">
      <div className="chart-title">
        <h3>{title}</h3>
        {last && <span className="chart-now">{format(last.v)}</span>}
      </div>
      {!geo ? (
        <div className="chart-empty">No data in this period</div>
      ) : (
        <div className="plot">
          <svg
            ref={ref}
            viewBox={`0 0 ${W} ${H}`}
            role="img"
            aria-label={`${title}: ${data.length} readings, latest ${format(last.v)}`}
            onPointerMove={onMove}
            onPointerLeave={() => setHover(null)}
            style={{ touchAction: "pan-y" }}
          >
            <clipPath id={clipId}>
              <rect x={M.left} y={0} width={W - M.left - M.right + 6} height={H - M.bottom + 1} />
            </clipPath>
            {geo.yTicks.map((v, i) => (
              <g key={i}>
                {i > 0 && <line className="gridline" x1={M.left} x2={W - M.right} y1={geo.y(v)} y2={geo.y(v)} />}
                <text className="tick" x={M.left - 8} y={geo.y(v)} dy="0.32em" textAnchor="end">
                  {format(v)}
                </text>
              </g>
            ))}
            <line className="baseline" x1={M.left} x2={W - M.right} y1={H - M.bottom} y2={H - M.bottom} />
            {geo.xTicks.map((t, i) => (
              <text
                key={i}
                className="tick"
                x={geo.x(t)}
                y={H - 6}
                textAnchor={i === 0 ? "start" : i === geo.xTicks.length - 1 ? "end" : "middle"}
              >
                {(geo.short ? hourFmt : dayFmt).format(t)}
              </text>
            ))}
            {threshold && threshold.value > geo.y0 && threshold.value < geo.y1 && (
              <line className="threshold" x1={M.left} x2={W - M.right} y1={geo.y(threshold.value)} y2={geo.y(threshold.value)}>
                <title>{threshold.label}</title>
              </line>
            )}
            <g clipPath={`url(#${clipId})`}>
              <path className="area" d={geo.area} />
              <path className="line" d={geo.line} />
            </g>
            {h ? (
              <>
                <line className="crosshair" x1={geo.x(h.t)} x2={geo.x(h.t)} y1={M.top} y2={H - M.bottom} />
                <circle className="hover-dot" cx={geo.x(h.t)} cy={geo.y(h.v)} r={5} />
              </>
            ) : (
              <circle className="end-dot" cx={geo.x(last.t)} cy={geo.y(last.v)} r={4.5} />
            )}
          </svg>
          {h && (
            <div
              className={geo.y(h.v) < H / 2 ? "tooltip below" : "tooltip"}
              style={{
                left: `clamp(70px, ${(geo.x(h.t) / W) * 100}%, calc(100% - 70px))`,
                top: geo.y(h.v) < H / 2 ? `calc(${(geo.y(h.v) / H) * 100}% + 12px)` : `calc(${(geo.y(h.v) / H) * 100}% - 12px)`,
              }}
            >
              <strong>{format(h.v)}</strong>
              <div className="subtle">{fullFmt.format(h.t)}</div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
