// Package health collects device health metrics. It only ever produces
// totals and statuses; no file names or paths are included.
package health

import "time"

// Metrics is the snapshot sent to the company dashboard. Pointer fields are
// nil when the value could not be determined on this machine.
type Metrics struct {
	DiskTotalBytes   uint64  `json:"disk_total_bytes"`
	DiskFreeBytes    uint64  `json:"disk_free_bytes"`
	DiskUsedPct      float64 `json:"disk_used_pct"`
	ReclaimableBytes *int64  `json:"reclaimable_bytes"`

	MemTotalBytes uint64  `json:"mem_total_bytes"`
	MemUsedPct    float64 `json:"mem_used_pct"`
	CPULoadPct    float64 `json:"cpu_load_pct"`
	UptimeSeconds uint64  `json:"uptime_seconds"`

	BatteryPresent   bool     `json:"battery_present"`
	BatteryHealthPct *float64 `json:"battery_health_pct"`
	BatteryCycles    *int     `json:"battery_cycle_count"`

	OSName         string `json:"os_name"`
	OSVersion      string `json:"os_version"`
	OSMajor        int    `json:"os_major"`
	PendingUpdates *int   `json:"pending_updates"`

	DiskEncrypted   *bool `json:"disk_encrypted"`
	FirewallEnabled *bool `json:"firewall_enabled"`

	AgentVersion string `json:"agent_version"`
}

type Snapshot struct {
	TakenAt time.Time `json:"taken_at"`
	Metrics Metrics   `json:"metrics"`
}
