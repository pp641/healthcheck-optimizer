// Package reporter enrolls the device with an organization and sends health
// snapshots to it. Snapshots are queued on disk so nothing is lost offline.
// Only health.Snapshot values are ever sent; file data stays on the laptop.
package reporter

import (
	"errors"
	"os"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/scanner"
)

const (
	enrollmentFile = "enrollment.json"
	lastSharedFile = "last-shared.json"
	lastScanFile   = "last-scan.json"
	updatesFile    = "updates-check.json"

	// A scan total older than this is not reported; it would mislead.
	maxScanAge = 14 * 24 * time.Hour
)

// Policy holds the health-reporting settings the organization controls.
type Policy struct {
	ReportIntervalMinutes int  `json:"report_interval_minutes"`
	AllowAI               bool `json:"allow_ai"`
}

// Interval returns the reporting interval, clamped to a sane range.
func (p Policy) Interval() time.Duration {
	m := p.ReportIntervalMinutes
	if m < 15 {
		m = 60
	}
	if m > 24*60 {
		m = 24 * 60
	}
	return time.Duration(m) * time.Minute
}

type Enrollment struct {
	ServerURL   string    `json:"server_url"`
	DeviceID    string    `json:"device_id"`
	DeviceToken string    `json:"device_token"`
	OrgName     string    `json:"org_name"`
	EnrolledAt  time.Time `json:"enrolled_at"`
	Policy      Policy    `json:"policy"`
}

// LoadEnrollment returns nil when the device is not enrolled.
func LoadEnrollment() (*Enrollment, error) {
	var e Enrollment
	if err := readState(enrollmentFile, &e); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

func saveEnrollment(e *Enrollment) error { return writeState(enrollmentFile, e) }

// ScanTotal is the only scan result kept for reporting: one number.
type ScanTotal struct {
	ScannedAt        time.Time `json:"scanned_at"`
	ReclaimableBytes int64     `json:"reclaimable_bytes"`
}

func SaveLastScan(rep scanner.Report) error {
	return writeState(lastScanFile, ScanTotal{ScannedAt: rep.ScannedAt, ReclaimableBytes: rep.ReclaimableBytes})
}

// AdjustLastScan subtracts freed space after a cleanup.
func AdjustLastScan(freed int64) error {
	t, err := LastScan()
	if err != nil || t == nil {
		return err
	}
	t.ReclaimableBytes -= freed
	if t.ReclaimableBytes < 0 {
		t.ReclaimableBytes = 0
	}
	return writeState(lastScanFile, t)
}

func LastScan() (*ScanTotal, error) {
	var t ScanTotal
	if err := readState(lastScanFile, &t); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// ReclaimableForReport returns the last scan total if it is recent enough.
func ReclaimableForReport() *int64 {
	t, err := LastScan()
	if err != nil || t == nil || time.Since(t.ScannedAt) > maxScanAge {
		return nil
	}
	v := t.ReclaimableBytes
	return &v
}

// UpdatesCheck caches the slow pending-updates lookup between snapshots.
type UpdatesCheck struct {
	CheckedAt time.Time `json:"checked_at"`
	Pending   *int      `json:"pending"`
}

func LoadUpdatesCheck() UpdatesCheck {
	var u UpdatesCheck
	_ = readState(updatesFile, &u)
	return u
}

func SaveUpdatesCheck(u UpdatesCheck) error { return writeState(updatesFile, u) }

func readState(name string, v any) error {
	p, err := config.Path(name)
	if err != nil {
		return err
	}
	return config.ReadJSON(p, v)
}

func writeState(name string, v any) error {
	p, err := config.Path(name)
	if err != nil {
		return err
	}
	return config.WriteJSON(p, v)
}

func removeState(name string) error {
	p, err := config.Path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
