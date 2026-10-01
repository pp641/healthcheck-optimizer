export type Item = {
  id: string;
  rule_id: string;
  rule_name: string;
  ecosystem: string;
  kind: "project" | "path" | "probe";
  path?: string;
  detail?: string;
  size: number;
  eligible: boolean;
  skip_reason?: string;
  stale_days: number;
  last_active?: string;
  git_dirty?: boolean;
  clean_method: string;
  action: string;
};

export type Entry = {
  name: string;
  path?: string;
  kind: "dir" | "files";
  size: number;
  files: number;
  has_children: boolean;
  collapsed?: boolean;
  locked?: boolean;
  denied?: boolean;
  item?: Item;
};

/**
 * Something selected on the Disk page: a rule-matched item (cleaned by its
 * rule) or any folder (can be moved to the Trash or moved by hand).
 */
export type Pick = { id: string; name: string; size: number; path?: string; item?: boolean };

export type AppState = {
  version: string;
  scanning: boolean;
  visited: number;
  scan_error: string;
  scan_roots: string[] | null;
  delete_mode: "trash" | "permanent";
  platform: string;
  home: string;
  org_name?: string;
  scanned_at?: string;
  reclaimable?: number;
  skipped?: number;
  eligible_count?: number;
  warnings?: string[];
  extras?: Item[];
  eligible_items?: Pick[];
  roots?: Entry[];
};

export type Config = {
  scan_roots: string[] | null;
  exclude: string[] | null;
  disabled_rules: string[] | null;
  stale_days: number;
  delete_mode: "trash" | "permanent";
  always_preview: boolean;
  clean_dirty_repos: boolean;
  max_depth: number;
  schedule: "manual" | "weekly";
  ai_mode: "off" | "local" | "cloud";
};

export type Rule = {
  id: string;
  name: string;
  ecosystem: string;
  description: string;
  kind: string;
  source: string;
  enabled: boolean;
  status: string;
  clean: { method: string };
};

export type LogEntry = {
  time: string;
  rule_id: string;
  path?: string;
  bytes: number;
  method: string;
  trashed_to?: string;
  ok: boolean;
  error?: string;
};

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
  pending_updates: number | null;
  disk_encrypted: boolean | null;
  firewall_enabled: boolean | null;
};

export type Sharing = {
  enrolled: boolean;
  queued: number;
  org_name?: string;
  server_url?: string;
  enrolled_at?: string;
  policy?: { report_interval_minutes: number; allow_ai: boolean };
  last_shared?: { sent_at: string; org_name: string; server_url: string; snapshot: unknown };
};

export type CleanResult = {
  entries: LogEntry[];
  freed: number;
  failed: number;
  skipped: string[];
  delete_mode: string;
  error?: string;
};
