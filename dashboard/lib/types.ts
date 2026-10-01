// Mirrors the Go server's JSON. Metrics are health totals only; the server
// never has file names or paths to send.

export type Metrics = {
  disk_total_bytes: number;
  disk_free_bytes: number;
  disk_used_pct: number;
  reclaimable_bytes: number | null;
  mem_total_bytes: number;
  mem_used_pct: number;
  cpu_load_pct: number;
  uptime_seconds: number;
  battery_present: boolean;
  battery_health_pct: number | null;
  battery_cycle_count: number | null;
  os_name: string;
  os_version: string;
  os_major: number;
  pending_updates: number | null;
  disk_encrypted: boolean | null;
  firewall_enabled: boolean | null;
  agent_version: string;
};

export type Device = {
  id: string;
  hostname: string;
  os_name: string;
  os_version: string;
  agent_version: string;
  enrolled_at: string;
  last_seen: string | null;
  last_snapshot_at: string | null;
  metrics: Metrics | null;
  os_versions_behind: number | null;
  open_alerts: number;
};

export type FleetSummary = {
  devices: number;
  low_disk: number;
  outdated_os: number;
  degraded_battery: number;
  encryption_off: number;
  firewall_off: number;
  offline: number;
  open_alerts: number;
  reclaimable_bytes: number;
};

export type Alert = {
  id: string;
  device_id: string;
  hostname: string;
  rule_id: string;
  rule_name: string;
  metric: string;
  status: "open" | "resolved";
  value: number | null;
  message: string;
  opened_at: string;
  resolved_at: string | null;
  notified_at: string | null;
};

export type AlertRule = {
  id: string;
  name: string;
  metric: string;
  op: string;
  threshold: number;
  enabled: boolean;
};

export type MetricDef = { key: string; label: string; unit: string; bool: boolean };

export type Org = {
  id: string;
  name: string;
  plan: string;
  enroll_code: string;
  report_interval_minutes: number;
  allow_ai: boolean;
  slack_webhook_url: string;
  created_at: string;
  device_count: number;
};

export type Me = {
  user: { id: string; org_id: string; email: string; role: "admin" | "viewer" };
  org_name: string;
};

export type Point = { taken_at: string; metrics: Metrics };
