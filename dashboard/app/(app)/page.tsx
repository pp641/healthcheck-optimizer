import type { Metadata } from "next";
import Link from "next/link";

import { Battery, DiskMeter, Encryption, PresenceStatus } from "@/components/device-bits";
import { StatusIcon, type Level } from "@/components/status";
import { api } from "@/lib/api";
import { bytes, osLabel } from "@/lib/format";
import type { Device, FleetSummary, Org } from "@/lib/types";

export const metadata: Metadata = { title: "Fleet" };

type Fleet = { summary: FleetSummary; devices: Device[] };

function Tile({ label, value, level, foot, href }: { label: string; value: string | number; level?: Level; foot: string; href?: string }) {
  const body = (
    <>
      <span className="tile-label">{label}</span>
      <span className="tile-value">{value}</span>
      <span className="tile-foot">
        {level && <StatusIcon level={level} />}
        {foot}
      </span>
    </>
  );
  return href ? (
    <Link className="tile" href={href}>
      {body}
    </Link>
  ) : (
    <div className="tile">{body}</div>
  );
}

function countTile(label: string, n: number, total: number, level: Level = "critical") {
  return <Tile label={label} value={n} level={n > 0 ? level : "good"} foot={n > 0 ? `of ${total} laptops` : "All clear"} />;
}

export default async function FleetPage() {
  const [fleet, org] = await Promise.all([api<Fleet>("/api/v1/fleet"), api<Org>("/api/v1/org")]);
  const s = fleet.summary;
  const needAttention = fleet.devices.filter((d) => d.open_alerts > 0).length;

  if (s.devices === 0) {
    return (
      <>
        <div className="page-head">
          <h1>Fleet</h1>
        </div>
        <div className="card empty">
          <h2>No laptops enrolled yet</h2>
          <p style={{ marginTop: 6 }}>Install the Tidyfleet agent on a laptop, then run:</p>
          {org.enroll_code ? (
            <code className="code-box" style={{ justifyContent: "center" }}>
              tidyfleet enroll &lt;server-url&gt; {org.enroll_code}
            </code>
          ) : (
            <p className="subtle">Ask an admin for your organization&apos;s enrollment code.</p>
          )}
        </div>
      </>
    );
  }

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Fleet</h1>
          <p className="subtle">
            {s.devices} laptop{s.devices === 1 ? "" : "s"} reporting every {org.report_interval_minutes} min
          </p>
        </div>
      </div>

      <div className="card">
        <div className="hero">
          <span className="hero-value">{needAttention}</span>
          <span className="hero-label">
            of {s.devices} laptops need attention
            {s.open_alerts > 0 && (
              <>
                {" · "}
                <Link href="/alerts">
                  {s.open_alerts} open alert{s.open_alerts === 1 ? "" : "s"}
                </Link>
              </>
            )}
          </span>
        </div>
      </div>

      <div className="tiles">
        {countTile("Disk almost full", s.low_disk, s.devices)}
        {countTile("OS 2+ versions behind", s.outdated_os, s.devices, "warning")}
        {countTile("Battery degraded", s.degraded_battery, s.devices, "warning")}
        {countTile("Encryption off", s.encryption_off, s.devices)}
        {countTile("Firewall off", s.firewall_off, s.devices, "warning")}
        {countTile("Not reporting (3+ days)", s.offline, s.devices, "warning")}
        <Tile label="Reclaimable dev space" value={bytes(s.reclaimable_bytes)} foot="old build output, caches" />
      </div>

      <section className="section">
        <h2>Laptops</h2>
        <div className="card table-wrap" style={{ padding: "6px 10px" }}>
          <table>
            <thead>
              <tr>
                <th>Device</th>
                <th>Disk used</th>
                <th className="num hide-sm">Reclaimable</th>
                <th className="hide-sm">Battery</th>
                <th>Encryption</th>
                <th>Last report</th>
                <th className="num">Alerts</th>
              </tr>
            </thead>
            <tbody>
              {fleet.devices.map((d) => (
                <tr key={d.id}>
                  <td>
                    <Link className="host" href={`/devices/${d.id}`}>
                      {d.hostname}
                    </Link>
                    <div className="subtle" style={{ fontSize: 12 }}>
                      {osLabel(d.os_name, d.os_version)}
                      {d.os_versions_behind != null && d.os_versions_behind >= 2 && ` · ${d.os_versions_behind} versions behind`}
                    </div>
                  </td>
                  <td>
                    <DiskMeter m={d.metrics} />
                  </td>
                  <td className="num hide-sm">{bytes(d.metrics?.reclaimable_bytes)}</td>
                  <td className="hide-sm">
                    <Battery m={d.metrics} />
                  </td>
                  <td>
                    <Encryption m={d.metrics} />
                  </td>
                  <td>
                    <PresenceStatus d={d} interval={org.report_interval_minutes} />
                  </td>
                  <td className="num">
                    {d.open_alerts > 0 ? <span className="pill critical">{d.open_alerts}</span> : <span className="subtle">0</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}
