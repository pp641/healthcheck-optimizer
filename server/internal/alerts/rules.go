// Package alerts evaluates an organization's alert rules against its devices
// and notifies Slack when alerts open or resolve.
package alerts

import (
	"fmt"
	"strings"
	"time"

	"github.com/tidyfleet/tidyfleet/server/internal/metrics"
)

// MetricDef describes a value a rule can test.
type MetricDef struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Unit  string `json:"unit"`
	// Bool metrics are 1 (true) or 0 (false).
	Bool bool `json:"bool"`
}

var Metrics = []MetricDef{
	{Key: "disk_used_pct", Label: "Disk used", Unit: "%"},
	{Key: "disk_free_gb", Label: "Disk free", Unit: "GB"},
	{Key: "reclaimable_gb", Label: "Reclaimable dev space", Unit: "GB"},
	{Key: "mem_used_pct", Label: "Memory used", Unit: "%"},
	{Key: "battery_health_pct", Label: "Battery health", Unit: "%"},
	{Key: "battery_cycle_count", Label: "Battery cycles", Unit: ""},
	{Key: "os_versions_behind", Label: "OS major versions behind", Unit: ""},
	{Key: "pending_updates", Label: "Pending OS updates", Unit: ""},
	{Key: "disk_encrypted", Label: "Disk encryption on", Bool: true},
	{Key: "firewall_enabled", Label: "Firewall on", Bool: true},
	{Key: "hours_since_seen", Label: "Hours since last report", Unit: "h"},
}

func MetricByKey(key string) (MetricDef, bool) {
	for _, m := range Metrics {
		if m.Key == key {
			return m, true
		}
	}
	return MetricDef{}, false
}

var Ops = map[string]string{"gt": ">", "gte": "≥", "lt": "<", "lte": "≤", "eq": "=", "neq": "≠"}

type Rule struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Metric    string  `json:"metric"`
	Op        string  `json:"op"`
	Threshold float64 `json:"threshold"`
	Enabled   bool    `json:"enabled"`
}

func (r Rule) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if _, ok := MetricByKey(r.Metric); !ok {
		return fmt.Errorf("unknown metric %q", r.Metric)
	}
	if _, ok := Ops[r.Op]; !ok {
		return fmt.Errorf("unknown operator %q", r.Op)
	}
	return nil
}

// Defaults are created with every new organization.
var Defaults = []Rule{
	{Name: "Disk almost full", Metric: "disk_used_pct", Op: "gt", Threshold: 90, Enabled: true},
	{Name: "OS two or more major versions behind", Metric: "os_versions_behind", Op: "gte", Threshold: 2, Enabled: true},
	{Name: "Battery degraded", Metric: "battery_health_pct", Op: "lt", Threshold: 70, Enabled: true},
	{Name: "Disk encryption off", Metric: "disk_encrypted", Op: "eq", Threshold: 0, Enabled: true},
	{Name: "Not reporting for 7 days", Metric: "hours_since_seen", Op: "gt", Threshold: 168, Enabled: true},
}

// DeviceState is what rules are evaluated against.
type DeviceState struct {
	Metrics  *metrics.Metrics
	LastSeen time.Time
	// LatestOSMajor is the newest major OS version known for this device's OS.
	LatestOSMajor int
}

// Value extracts a rule's metric. ok is false when the device has not
// reported it (e.g. battery health on a desktop), and the rule is skipped.
func Value(metric string, d DeviceState, now time.Time) (v float64, ok bool) {
	if metric == "hours_since_seen" {
		if d.LastSeen.IsZero() {
			return 0, false
		}
		return now.Sub(d.LastSeen).Hours(), true
	}
	m := d.Metrics
	if m == nil {
		return 0, false
	}
	b2f := func(b *bool) (float64, bool) {
		if b == nil {
			return 0, false
		}
		if *b {
			return 1, true
		}
		return 0, true
	}
	switch metric {
	case "disk_used_pct":
		return m.DiskUsedPct, m.DiskTotalBytes > 0
	case "disk_free_gb":
		return float64(m.DiskFreeBytes) / 1e9, m.DiskTotalBytes > 0
	case "reclaimable_gb":
		if m.ReclaimableBytes == nil {
			return 0, false
		}
		return float64(*m.ReclaimableBytes) / 1e9, true
	case "mem_used_pct":
		return m.MemUsedPct, m.MemTotalBytes > 0
	case "battery_health_pct":
		if m.BatteryHealthPct == nil {
			return 0, false
		}
		return *m.BatteryHealthPct, true
	case "battery_cycle_count":
		if m.BatteryCycles == nil {
			return 0, false
		}
		return float64(*m.BatteryCycles), true
	case "os_versions_behind":
		if m.OSMajor <= 0 || d.LatestOSMajor <= 0 {
			return 0, false
		}
		return float64(metrics.VersionsBehind(m.OSName, m.OSMajor, d.LatestOSMajor)), true
	case "pending_updates":
		if m.PendingUpdates == nil {
			return 0, false
		}
		return float64(*m.PendingUpdates), true
	case "disk_encrypted":
		return b2f(m.DiskEncrypted)
	case "firewall_enabled":
		return b2f(m.FirewallEnabled)
	}
	return 0, false
}

func Compare(v float64, op string, threshold float64) bool {
	switch op {
	case "gt":
		return v > threshold
	case "gte":
		return v >= threshold
	case "lt":
		return v < threshold
	case "lte":
		return v <= threshold
	case "eq":
		return v == threshold
	case "neq":
		return v != threshold
	}
	return false
}

// Describe renders a firing alert, e.g. "Disk almost full: disk used 93.1%".
func Describe(r Rule, v float64) string {
	def, _ := MetricByKey(r.Metric)
	if def.Bool {
		state := "off"
		if v == 1 {
			state = "on"
		}
		return fmt.Sprintf("%s: %s is %s", r.Name, strings.ToLower(strings.TrimSuffix(def.Label, " on")), state)
	}
	return fmt.Sprintf("%s: %s %s%s", r.Name, strings.ToLower(def.Label), formatNum(v), def.Unit)
}

func formatNum(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.1f", v)
}
