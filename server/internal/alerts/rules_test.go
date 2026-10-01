package alerts

import (
	"testing"
	"time"

	"github.com/tidyfleet/tidyfleet/server/internal/metrics"
)

func ptr[T any](v T) *T { return &v }

func TestValueAndCompare(t *testing.T) {
	now := time.Now()
	m := &metrics.Metrics{
		DiskTotalBytes: 500e9, DiskFreeBytes: 25e9, DiskUsedPct: 95,
		BatteryHealthPct: ptr(65.0), OSName: "macos", OSMajor: 13,
		DiskEncrypted: ptr(false), ReclaimableBytes: ptr(int64(40e9)),
	}
	st := DeviceState{Metrics: m, LastSeen: now.Add(-200 * time.Hour), LatestOSMajor: 26}

	for _, r := range Defaults {
		v, ok := Value(r.Metric, st, now)
		if !ok {
			t.Errorf("%s: value unknown", r.Metric)
			continue
		}
		if !Compare(v, r.Op, r.Threshold) {
			t.Errorf("default rule %q should fire for value %v", r.Name, v)
		}
	}

	if v, _ := Value("reclaimable_gb", st, now); v != 40 {
		t.Errorf("reclaimable_gb = %v", v)
	}
	if _, ok := Value("firewall_enabled", st, now); ok {
		t.Error("unknown firewall state should not be evaluated")
	}
	if _, ok := Value("battery_health_pct", DeviceState{Metrics: &metrics.Metrics{}}, now); ok {
		t.Error("desktop without battery should not be evaluated")
	}
	if _, ok := Value("disk_used_pct", DeviceState{}, now); ok {
		t.Error("device without metrics should not be evaluated")
	}
	if v, _ := Value("os_versions_behind", st, now); v != 3 { // 13 -> 14, 15, 26
		t.Errorf("macOS 13 vs 26: %v versions behind, want 3", v)
	}
	sequoia := DeviceState{Metrics: &metrics.Metrics{OSName: "macos", OSMajor: 15}, LatestOSMajor: 26}
	if v, _ := Value("os_versions_behind", sequoia, now); v != 1 {
		t.Errorf("macOS 15 vs 26: %v versions behind, want 1", v)
	}
	healthy := DeviceState{Metrics: &metrics.Metrics{OSMajor: 26}, LatestOSMajor: 26}
	if v, _ := Value("os_versions_behind", healthy, now); v != 0 {
		t.Errorf("os_versions_behind = %v, want 0", v)
	}
}

func TestDescribe(t *testing.T) {
	cases := []struct {
		r    Rule
		v    float64
		want string
	}{
		{Rule{Name: "Disk almost full", Metric: "disk_used_pct"}, 93.14, "Disk almost full: disk used 93.1%"},
		{Rule{Name: "Encryption", Metric: "disk_encrypted"}, 0, "Encryption: disk encryption is off"},
		{Rule{Name: "Old OS", Metric: "os_versions_behind"}, 3, "Old OS: os major versions behind 3"},
	}
	for _, c := range cases {
		if got := Describe(c.r, c.v); got != c.want {
			t.Errorf("Describe = %q, want %q", got, c.want)
		}
	}
}

func TestRuleValidate(t *testing.T) {
	if err := (Rule{Name: "x", Metric: "disk_used_pct", Op: "gt"}).Validate(); err != nil {
		t.Error(err)
	}
	for _, r := range []Rule{
		{Metric: "disk_used_pct", Op: "gt"},
		{Name: "x", Metric: "file_names", Op: "gt"},
		{Name: "x", Metric: "disk_used_pct", Op: "like"},
	} {
		if r.Validate() == nil {
			t.Errorf("%+v should be invalid", r)
		}
	}
}

func TestValidWebhook(t *testing.T) {
	if !ValidWebhook("https://hooks.slack.com/services/T/B/x") || !ValidWebhook("") {
		t.Error("valid webhook rejected")
	}
	for _, u := range []string{"http://hooks.slack.com/x", "https://evil.example/hooks.slack.com/", "http://169.254.169.254/"} {
		if ValidWebhook(u) {
			t.Errorf("%s should be rejected", u)
		}
	}
}
