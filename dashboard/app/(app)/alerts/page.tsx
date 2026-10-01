import type { Metadata } from "next";
import Link from "next/link";

import { NewRuleForm, RuleRow } from "@/components/forms";
import { Status } from "@/components/status";
import { api } from "@/lib/api";
import { dateTime, relTime } from "@/lib/format";
import type { Alert, AlertRule, Me, MetricDef } from "@/lib/types";

export const metadata: Metadata = { title: "Alerts" };

const TABS = ["open", "resolved", "all"] as const;

type Props = { searchParams: Promise<{ status?: string }> };

export default async function AlertsPage({ searchParams }: Props) {
  const q = (await searchParams).status;
  const status = TABS.find((t) => t === q) ?? "open";
  const [alerts, rules, me] = await Promise.all([
    api<{ alerts: Alert[] }>(`/api/v1/alerts?status=${status}`),
    api<{ rules: AlertRule[]; metrics: MetricDef[]; ops: Record<string, string> }>("/api/v1/alert-rules"),
    api<Me>("/api/v1/me"),
  ]);
  const isAdmin = me.user.role === "admin";

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Alerts</h1>
          <p className="subtle">Checked every time a laptop reports, and every 5 minutes for laptops that stop reporting.</p>
        </div>
        <nav className="segmented" aria-label="Alert status">
          {TABS.map((t) => (
            <Link key={t} href={`?status=${t}`} aria-current={t === status ? "true" : undefined}>
              {t[0].toUpperCase() + t.slice(1)}
            </Link>
          ))}
        </nav>
      </div>

      <div className="card table-wrap" style={{ padding: "6px 10px" }}>
        {alerts.alerts.length === 0 ? (
          <div className="empty">
            <Status level="good">{status === "open" ? "No open alerts. Every laptop is within your rules." : "Nothing here yet."}</Status>
          </div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Status</th>
                <th>Device</th>
                <th>Alert</th>
                <th>Opened</th>
                <th className="hide-sm">Resolved</th>
                <th className="hide-sm">Slack</th>
              </tr>
            </thead>
            <tbody>
              {alerts.alerts.map((a) => (
                <tr key={a.id}>
                  <td>{a.status === "open" ? <Status level="critical">Open</Status> : <Status level="good">Resolved</Status>}</td>
                  <td>
                    <Link className="host" href={`/devices/${a.device_id}`}>
                      {a.hostname}
                    </Link>
                  </td>
                  <td>{a.message}</td>
                  <td title={dateTime(a.opened_at)}>{relTime(a.opened_at)}</td>
                  <td className="hide-sm" title={dateTime(a.resolved_at)}>
                    {a.resolved_at ? relTime(a.resolved_at) : "—"}
                  </td>
                  <td className="hide-sm subtle">{a.notified_at ? "Sent" : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <section className="section">
        <h2>Rules</h2>
        <div className="card table-wrap" style={{ padding: "6px 10px" }}>
          <table>
            <thead>
              <tr>
                <th>Rule</th>
                <th>Condition</th>
                <th className="num">Threshold</th>
                <th>Status</th>
                {isAdmin && <th />}
              </tr>
            </thead>
            <tbody>
              {rules.rules.map((r) => (
                <RuleRow key={`${r.id}-${r.threshold}-${r.enabled}`} rule={r} metrics={rules.metrics} ops={rules.ops} canEdit={isAdmin} />
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {isAdmin && (
        <section className="section">
          <h2>Add a rule</h2>
          <div className="card">
            <NewRuleForm metrics={rules.metrics} ops={rules.ops} />
          </div>
        </section>
      )}
    </>
  );
}
