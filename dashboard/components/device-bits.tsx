import { Status, StatusIcon, type Level } from "@/components/status";
import { bytes, pct, presence, relTime } from "@/lib/format";
import type { Device } from "@/lib/types";

export function DiskMeter({ m }: { m: Device["metrics"] }) {
  if (!m || !m.disk_total_bytes) return <span className="subtle">—</span>;
  const used = m.disk_used_pct;
  const sev = used >= 90 ? "critical" : used >= 80 ? "warning" : "";
  return (
    <div className="meter" title={`${bytes(m.disk_free_bytes)} free of ${bytes(m.disk_total_bytes)}`}>
      <div className="meter-track" role="meter" aria-valuenow={used} aria-valuemin={0} aria-valuemax={100} aria-label="Disk used">
        <div className={`meter-fill ${sev}`} style={{ width: `${Math.min(used, 100)}%` }} />
      </div>
      <span className="meter-label">{pct(used)}</span>
      {sev && <StatusIcon level={sev as Level} title={sev === "critical" ? "Almost full" : "Filling up"} />}
    </div>
  );
}

export function PresenceStatus({ d, interval }: { d: Device; interval: number }) {
  const p = presence(d, interval);
  const level: Level = p === "online" ? "good" : p === "late" ? "warning" : "critical";
  return <Status level={level}>{relTime(d.last_seen)}</Status>;
}

export function Encryption({ m }: { m: Device["metrics"] }) {
  if (m?.disk_encrypted == null) return <Status level="unknown">Unknown</Status>;
  return m.disk_encrypted ? <Status level="good">On</Status> : <Status level="critical">Off</Status>;
}

export function Battery({ m }: { m: Device["metrics"] }) {
  if (!m?.battery_present) return <span className="subtle">No battery</span>;
  if (m.battery_health_pct == null) return <Status level="unknown">Unknown</Status>;
  const level: Level = m.battery_health_pct < 70 ? "critical" : m.battery_health_pct < 80 ? "warning" : "good";
  return <Status level={level}>{pct(m.battery_health_pct)}</Status>;
}
