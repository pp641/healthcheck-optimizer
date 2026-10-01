"use client";

import { useCallback, useEffect, useState } from "react";

import { useApp } from "@/components/app-context";
import { api, errorText } from "@/lib/api";
import { bytes, dateTime, relTime } from "@/lib/format";
import type { Metrics, Sharing } from "@/lib/types";

function onOff(v: boolean | null, on = "On", off = "Off") {
  return v == null ? "Unknown" : v ? on : off;
}

export default function SharingPage() {
  const { toast, refresh } = useApp();
  const [health, setHealth] = useState<{ metrics: Metrics } | null>(null);
  const [share, setShare] = useState<Sharing | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [server, setServer] = useState("");
  const [code, setCode] = useState("");

  const load = useCallback(async () => {
    try {
      setShare(await api<Sharing>("/api/sharing"));
    } catch (e) {
      setErr(errorText(e));
    }
  }, []);
  useEffect(() => {
    load();
    api<{ metrics: Metrics }>("/api/health").then(setHealth).catch(() => {});
  }, [load]);

  async function act(fn: () => Promise<Sharing>, done: string) {
    setBusy(true);
    setErr(null);
    try {
      setShare(await fn());
      toast(done);
      refresh();
    } catch (e) {
      setErr(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  const m = health?.metrics;
  return (
    <div className="settings-grid">
      <div className="card">
        <div className="section-head" style={{ padding: "18px 20px 0" }}>
          <h2>This laptop</h2>
          <p className="subtle">Health numbers, measured now.</p>
        </div>
        {!m ? (
          <p className="empty subtle">Measuring…</p>
        ) : (
          <dl className="kv">
            <div>
              <dt>Disk</dt>
              <dd>
                {m.disk_used_pct.toFixed(0)}% used · {bytes(m.disk_free_bytes)} free
              </dd>
            </div>
            <div>
              <dt>Reclaimable dev space</dt>
              <dd>{bytes(m.reclaimable_bytes)}</dd>
            </div>
            <div>
              <dt>Memory</dt>
              <dd>
                {m.mem_used_pct.toFixed(0)}% of {bytes(m.mem_total_bytes)}
              </dd>
            </div>
            <div>
              <dt>Battery</dt>
              <dd>
                {!m.battery_present
                  ? "No battery"
                  : `${m.battery_health_pct?.toFixed(0) ?? "?"}% health${m.battery_cycle_count != null ? ` · ${m.battery_cycle_count} cycles` : ""}`}
              </dd>
            </div>
            <div>
              <dt>Operating system</dt>
              <dd>
                {m.os_name === "macos" ? "macOS" : m.os_name} {m.os_version}
              </dd>
            </div>
            <div>
              <dt>Pending updates</dt>
              <dd>{m.pending_updates ?? "Not checked"}</dd>
            </div>
            <div>
              <dt>Disk encryption</dt>
              <dd>{onOff(m.disk_encrypted)}</dd>
            </div>
            <div>
              <dt>Firewall</dt>
              <dd>{onOff(m.firewall_enabled)}</dd>
            </div>
          </dl>
        )}
      </div>

      {err && (
        <p className="notice msg error" role="alert">
          {err}
        </p>
      )}

      {share?.enrolled ? (
        <div className="settings-card card">
          <div>
            <h2>Shared with {share.org_name}</h2>
            <p className="subtle">
              Reporting every {share.policy?.report_interval_minutes} min to {share.server_url}. Enrolled {dateTime(share.enrolled_at)}.
              {share.queued > 0 && ` ${share.queued} snapshots are waiting to be sent.`}
            </p>
          </div>
          <div>
            <h3 style={{ marginBottom: 6 }}>
              Exactly what was last sent {share.last_shared ? `(${relTime(share.last_shared.sent_at)})` : ""}
            </h3>
            {share.last_shared ? (
              <pre className="json">{JSON.stringify(share.last_shared.snapshot, null, 2)}</pre>
            ) : (
              <p className="subtle">Nothing sent yet.</p>
            )}
          </div>
          <div className="form-row">
            <button className="btn" disabled={busy} onClick={() => act(() => api<Sharing>("/api/report", {}), "Snapshot sent")}>
              Send now
            </button>
            <button
              className="btn danger"
              disabled={busy}
              onClick={() => {
                if (confirm(`Leave ${share.org_name}? Reporting stops and the organization's copy of this laptop's history is deleted.`)) {
                  act(() => api<Sharing>("/api/leave", {}), "Left the organization");
                }
              }}
            >
              Leave organization
            </button>
          </div>
        </div>
      ) : (
        <div className="settings-card card">
          <div>
            <h2>Not shared with anyone</h2>
            <p className="subtle">The cleaner works on its own. If your company uses Tidyfleet, enroll with the code from your admin.</p>
          </div>
          <form
            className="form-row"
            onSubmit={(e) => {
              e.preventDefault();
              act(() => api<Sharing>("/api/enroll", { server_url: server, code }), "Enrolled");
            }}
          >
            <label className="field" style={{ flex: 2, minWidth: 220 }}>
              Server
              <input type="url" required value={server} onChange={(e) => setServer(e.target.value)} placeholder="https://fleet.example.com" />
            </label>
            <label className="field" style={{ flex: 1, minWidth: 160 }}>
              Enrollment code
              <input type="text" required value={code} onChange={(e) => setCode(e.target.value)} placeholder="TF-XXXXX-XXXXX" />
            </label>
            <button className="btn primary" type="submit" disabled={busy}>
              Enroll
            </button>
          </form>
        </div>
      )}

      <div className="settings-card card">
        <h2>What an organization can see</h2>
        <div className="two-col">
          <div>
            <h3>Shared when enrolled</h3>
            <ul>
              <li>Disk used and free, plus one total for reclaimable dev space</li>
              <li>Memory, CPU load, uptime</li>
              <li>Battery health, OS version, pending updates</li>
              <li>Disk encryption and firewall status</li>
            </ul>
          </div>
          <div>
            <h3>Never leaves this laptop</h3>
            <ul>
              <li>File and folder names, paths, the disk tree</li>
              <li>Your cleanup history</li>
              <li>Your cleaner settings, which only you control</li>
            </ul>
          </div>
        </div>
      </div>
    </div>
  );
}
