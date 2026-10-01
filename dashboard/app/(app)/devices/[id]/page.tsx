import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { Battery, Encryption, PresenceStatus } from "@/components/device-bits";
import { RemoveDeviceForm } from "@/components/forms";
import { LineChart, type Datum } from "@/components/line-chart";
import { Status } from "@/components/status";
import { api, ApiError } from "@/lib/api";
import { bytes, dateTime, osLabel, pct, relTime } from "@/lib/format";
import type { Alert, AlertRule, Device, Me, Metrics, Org, Point } from "@/lib/types";

export const metadata: Metadata = { title: "Device" };

const RANGES = [7, 14, 30, 90];

type Props = { params: Promise<{ id: string }>; searchParams: Promise<{ days?: string }> };

function ruleThreshold(rules: AlertRule[], metric: string) {
  const r = rules.find((x) => x.enabled && x.metric === metric && (x.op === "gt" || x.op === "gte" || x.op === "lt" || x.op === "lte"));
  return r ? { value: r.threshold, label: `Alert: ${r.name}` } : undefined;
}

function series(points: Point[], pick: (m: Metrics) => number | null | undefined): Datum[] {
  const out: Datum[] = [];
  for (const p of points) {
    const v = pick(p.metrics);
    if (v != null) out.push({ t: new Date(p.taken_at).getTime(), v });
  }
  return out;
}

export default async function DevicePage({ params, searchParams }: Props) {
  const { id } = await params;
  const days = RANGES.includes(Number((await searchParams).days)) ? Number((await searchParams).days) : 14;

  let detail: { device: Device; alerts: Alert[] };
  try {
    detail = await api(`/api/v1/devices/${encodeURIComponent(id)}`);
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) notFound();
    throw e;
  }
  const [hist, me, org, rules] = await Promise.all([
    api<{ points: Point[] }>(`/api/v1/devices/${encodeURIComponent(id)}/snapshots?days=${days}`),
    api<Me>("/api/v1/me"),
    api<Org>("/api/v1/org"),
    api<{ rules: AlertRule[] }>("/api/v1/alert-rules"),
  ]);
  const d = detail.device;
  const m = d.metrics;
  const pts = hist.points;

  const battery = series(pts, (x) => x.battery_health_pct);
  const reclaim = series(pts, (x) => (x.reclaimable_bytes == null ? null : x.reclaimable_bytes / 1e9));

  return (
    <>
      <p style={{ marginBottom: 8 }}>
        <Link href="/">← Fleet</Link>
      </p>
      <div className="page-head">
        <div>
          <h1>{d.hostname}</h1>
          <p className="subtle">
            {osLabel(d.os_name, d.os_version)} · agent {d.agent_version || "unknown"} · enrolled {dateTime(d.enrolled_at)}
          </p>
        </div>
        <PresenceStatus d={d} interval={org.report_interval_minutes} />
      </div>

      <div className="card">
        {!m ? (
          <p className="subtle">This laptop has enrolled but not sent a health snapshot yet.</p>
        ) : (
          <dl className="kv" style={{ margin: 0 }}>
            <div>
              <dt>Disk</dt>
              <dd>
                {pct(m.disk_used_pct)} used · {bytes(m.disk_free_bytes)} free of {bytes(m.disk_total_bytes)}
              </dd>
            </div>
            <div>
              <dt>Reclaimable dev space</dt>
              <dd>{m.reclaimable_bytes == null ? "Not scanned recently" : bytes(m.reclaimable_bytes)}</dd>
            </div>
            <div>
              <dt>Memory</dt>
              <dd>
                {pct(m.mem_used_pct)} of {bytes(m.mem_total_bytes)}
              </dd>
            </div>
            <div>
              <dt>Battery</dt>
              <dd>
                <Battery m={m} />
                {m.battery_cycle_count != null && <span className="subtle"> · {m.battery_cycle_count} cycles</span>}
              </dd>
            </div>
            <div>
              <dt>Operating system</dt>
              <dd>
                {d.os_versions_behind != null && d.os_versions_behind >= 2 ? (
                  <Status level="warning">
                    {osLabel(m.os_name, m.os_version)} ({d.os_versions_behind} major versions behind)
                  </Status>
                ) : (
                  osLabel(m.os_name, m.os_version)
                )}
              </dd>
            </div>
            <div>
              <dt>Pending updates</dt>
              <dd>{m.pending_updates ?? "Unknown"}</dd>
            </div>
            <div>
              <dt>Disk encryption</dt>
              <dd>
                <Encryption m={m} />
              </dd>
            </div>
            <div>
              <dt>Firewall</dt>
              <dd>
                {m.firewall_enabled == null ? (
                  <Status level="unknown">Unknown</Status>
                ) : m.firewall_enabled ? (
                  <Status level="good">On</Status>
                ) : (
                  <Status level="warning">Off</Status>
                )}
              </dd>
            </div>
            <div>
              <dt>Uptime</dt>
              <dd>
                {Math.floor(m.uptime_seconds / 86400)} d {Math.floor((m.uptime_seconds % 86400) / 3600)} h
              </dd>
            </div>
          </dl>
        )}
      </div>

      <section className="section">
        <h2>Open alerts</h2>
        {detail.alerts.length === 0 ? (
          <div className="card">
            <Status level="good">No open alerts</Status>
          </div>
        ) : (
          <div className="card stack">
            {detail.alerts.map((a) => (
              <div key={a.id} className="row" style={{ justifyContent: "space-between" }}>
                <Status level="critical">{a.message}</Status>
                <span className="subtle">since {relTime(a.opened_at)}</span>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="section">
        <div className="card-head" style={{ marginBottom: 10 }}>
          <h2>History</h2>
          <nav className="segmented" aria-label="Time range">
            {RANGES.map((r) => (
              <Link key={r} href={`?days=${r}`} aria-current={r === days ? "true" : undefined} scroll={false}>
                {r} days
              </Link>
            ))}
          </nav>
        </div>
        <div className="charts">
          <LineChart title="Disk used" data={series(pts, (x) => x.disk_used_pct)} unit="pct" domain={[0, 100]} threshold={ruleThreshold(rules.rules, "disk_used_pct")} />
          <LineChart title="Memory used" data={series(pts, (x) => x.mem_used_pct)} unit="pct" domain={[0, 100]} />
          {reclaim.length > 0 && <LineChart title="Reclaimable dev space" data={reclaim} unit="gb" />}
          {battery.length > 0 && (
            <LineChart title="Battery health" data={battery} unit="pct" domain={[0, 100]} threshold={ruleThreshold(rules.rules, "battery_health_pct")} />
          )}
          <LineChart title="CPU load" data={series(pts, (x) => x.cpu_load_pct)} unit="pct" domain={[0, 100]} />
        </div>
        {pts.length > 0 && (
          <details style={{ marginTop: 12 }}>
            <summary>Show readings as a table</summary>
            <div className="card table-wrap" style={{ marginTop: 8, padding: "6px 10px" }}>
              <table>
                <thead>
                  <tr>
                    <th>Time</th>
                    <th className="num">Disk used</th>
                    <th className="num">Free</th>
                    <th className="num">Memory</th>
                    <th className="num">CPU</th>
                    <th className="num">Battery</th>
                    <th className="num">Reclaimable</th>
                  </tr>
                </thead>
                <tbody>
                  {pts
                    .slice(-200)
                    .reverse()
                    .map((p) => (
                      <tr key={p.taken_at}>
                        <td>{dateTime(p.taken_at)}</td>
                        <td className="num">{pct(p.metrics.disk_used_pct, 1)}</td>
                        <td className="num">{bytes(p.metrics.disk_free_bytes)}</td>
                        <td className="num">{pct(p.metrics.mem_used_pct)}</td>
                        <td className="num">{pct(p.metrics.cpu_load_pct)}</td>
                        <td className="num">{pct(p.metrics.battery_health_pct)}</td>
                        <td className="num">{bytes(p.metrics.reclaimable_bytes)}</td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
          </details>
        )}
      </section>

      <section className="section">
        <h2>What this dashboard can see</h2>
        <p className="subtle" style={{ maxWidth: 680 }}>
          Health totals only. File and folder names, the disk tree and cleanup history never leave the laptop. The employee can see the exact last snapshot with{" "}
          <code>tidyfleet shared</code>.
        </p>
        {me.user.role === "admin" && (
          <div style={{ marginTop: 16 }}>
            <RemoveDeviceForm id={d.id} hostname={d.hostname} />
          </div>
        )}
      </section>
    </>
  );
}
