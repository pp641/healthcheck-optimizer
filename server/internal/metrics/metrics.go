// Package metrics defines the health snapshot contract between the agent and
// the server. Snapshots are decoded into this fixed struct and re-encoded
// before storage, so any field the server does not know (a file path, say)
// is dropped instead of stored.
package metrics

import (
	"errors"
	"math"
	"strings"
	"time"
)

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

const maxAge = 90 * 24 * time.Hour

// VersionsBehind counts major releases between a device's OS and the latest.
// macOS jumped from 15 to 26 in 2025 (year-based numbering), so 26 counts as
// the release after 15.
func VersionsBehind(osName string, major, latest int) int {
	rank := func(v int) int {
		if osName == "macos" && v >= 26 {
			return v - 10
		}
		return v
	}
	return max(rank(latest)-rank(major), 0)
}

// Validate rejects snapshots that are malformed or implausible and trims
// free-text fields.
func (s *Snapshot) Validate(now time.Time) error {
	if s.TakenAt.IsZero() {
		return errors.New("taken_at is required")
	}
	if s.TakenAt.After(now.Add(time.Hour)) {
		return errors.New("taken_at is in the future")
	}
	if s.TakenAt.Before(now.Add(-maxAge)) {
		return errors.New("taken_at is too old")
	}
	m := &s.Metrics
	for _, pct := range []float64{m.DiskUsedPct, m.MemUsedPct, m.CPULoadPct} {
		if math.IsNaN(pct) || pct < 0 || pct > 100 {
			return errors.New("percentages must be between 0 and 100")
		}
	}
	if m.BatteryHealthPct != nil && (*m.BatteryHealthPct < 0 || *m.BatteryHealthPct > 200) {
		return errors.New("battery_health_pct out of range")
	}
	if m.ReclaimableBytes != nil && *m.ReclaimableBytes < 0 {
		return errors.New("reclaimable_bytes must not be negative")
	}
	m.OSName = clip(m.OSName, 32)
	m.OSVersion = clip(m.OSVersion, 64)
	m.AgentVersion = clip(m.AgentVersion, 32)
	return nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n]
	}
	return s
}
