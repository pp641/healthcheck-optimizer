package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tidyfleet/tidyfleet/server/internal/alerts"
	"github.com/tidyfleet/tidyfleet/server/internal/metrics"
)

// Thresholds for the fleet overview tiles. They match the default alert
// rules; the alert rules themselves are what each org customizes.
const (
	lowDiskPct       = 90
	outdatedBehind   = 2
	degradedBattery  = 70
	offlineAfter     = 72 * time.Hour
	maxHistoryPoints = 1000
)

type deviceJSON struct {
	ID               string           `json:"id"`
	Hostname         string           `json:"hostname"`
	OSName           string           `json:"os_name"`
	OSVersion        string           `json:"os_version"`
	AgentVersion     string           `json:"agent_version"`
	EnrolledAt       time.Time        `json:"enrolled_at"`
	LastSeen         *time.Time       `json:"last_seen"`
	LastSnapshotAt   *time.Time       `json:"last_snapshot_at"`
	Metrics          *metrics.Metrics `json:"metrics"`
	OSVersionsBehind *int             `json:"os_versions_behind"`
	OpenAlerts       int              `json:"open_alerts"`
}

const deviceCols = `d.id, d.hostname, d.os_name, d.os_version, d.agent_version, d.enrolled_at, d.last_seen,
	d.last_snapshot_at, d.last_metrics,
	(SELECT count(*) FROM alerts a WHERE a.device_id = d.id AND a.status = 'open')`

func scanDevice(row pgx.Row, d *deviceJSON) error {
	return row.Scan(&d.ID, &d.Hostname, &d.OSName, &d.OSVersion, &d.AgentVersion, &d.EnrolledAt, &d.LastSeen,
		&d.LastSnapshotAt, &d.Metrics, &d.OpenAlerts)
}

func setBehind(d *deviceJSON, latest map[string]int) {
	if d.Metrics == nil || d.Metrics.OSMajor <= 0 || latest[d.Metrics.OSName] <= 0 {
		return
	}
	b := metrics.VersionsBehind(d.Metrics.OSName, d.Metrics.OSMajor, latest[d.Metrics.OSName])
	d.OSVersionsBehind = &b
}

type fleetSummary struct {
	Devices          int   `json:"devices"`
	LowDisk          int   `json:"low_disk"`
	OutdatedOS       int   `json:"outdated_os"`
	DegradedBattery  int   `json:"degraded_battery"`
	EncryptionOff    int   `json:"encryption_off"`
	FirewallOff      int   `json:"firewall_off"`
	Offline          int   `json:"offline"`
	OpenAlerts       int   `json:"open_alerts"`
	ReclaimableBytes int64 `json:"reclaimable_bytes"`
}

func (s *Server) fleet(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	rows, err := s.DB.Query(r.Context(), `SELECT `+deviceCols+` FROM devices d WHERE d.org_id = $1 ORDER BY lower(d.hostname)`, u.OrgID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	devices, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (deviceJSON, error) {
		var d deviceJSON
		return d, scanDevice(row, &d)
	})
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	latest, err := s.Alerts.LatestOSMajors(r.Context(), u.OrgID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	sum := fleetSummary{Devices: len(devices)}
	now := time.Now()
	for i := range devices {
		d := &devices[i]
		setBehind(d, latest)
		sum.OpenAlerts += d.OpenAlerts
		if d.LastSeen == nil || now.Sub(*d.LastSeen) > offlineAfter {
			sum.Offline++
		}
		m := d.Metrics
		if m == nil {
			continue
		}
		if m.DiskTotalBytes > 0 && m.DiskUsedPct >= lowDiskPct {
			sum.LowDisk++
		}
		if d.OSVersionsBehind != nil && *d.OSVersionsBehind >= outdatedBehind {
			sum.OutdatedOS++
		}
		if m.BatteryHealthPct != nil && *m.BatteryHealthPct < degradedBattery {
			sum.DegradedBattery++
		}
		if m.DiskEncrypted != nil && !*m.DiskEncrypted {
			sum.EncryptionOff++
		}
		if m.FirewallEnabled != nil && !*m.FirewallEnabled {
			sum.FirewallOff++
		}
		if m.ReclaimableBytes != nil {
			sum.ReclaimableBytes += *m.ReclaimableBytes
		}
	}
	if devices == nil {
		devices = []deviceJSON{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": sum, "devices": devices, "latest_os": latest})
}

// orgDevice loads one device, scoped to the caller's organization.
func (s *Server) orgDevice(w http.ResponseWriter, r *http.Request) (deviceJSON, bool) {
	var d deviceJSON
	id, ok := pathID(w, r)
	if !ok {
		return d, false
	}
	u := userFrom(r)
	err := scanDevice(s.DB.QueryRow(r.Context(), `SELECT `+deviceCols+` FROM devices d WHERE d.id = $1 AND d.org_id = $2`, id, u.OrgID), &d)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "device not found")
		return d, false
	}
	if err != nil {
		s.internalErr(w, r, err)
		return d, false
	}
	return d, true
}

func (s *Server) device(w http.ResponseWriter, r *http.Request) {
	d, ok := s.orgDevice(w, r)
	if !ok {
		return
	}
	latest, err := s.Alerts.LatestOSMajors(r.Context(), userFrom(r).OrgID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	setBehind(&d, latest)
	list, err := s.queryAlerts(r, "open", d.ID, 100)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"device": d, "alerts": list})
}

type point struct {
	TakenAt time.Time       `json:"taken_at"`
	Metrics metrics.Metrics `json:"metrics"`
}

func (s *Server) deviceSnapshots(w http.ResponseWriter, r *http.Request) {
	d, ok := s.orgDevice(w, r)
	if !ok {
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 {
		days = 14
	}
	days = min(days, 90)
	rows, err := s.DB.Query(r.Context(), `SELECT taken_at, metrics FROM device_snapshots
		WHERE device_id = $1 AND taken_at > now() - make_interval(days => $2) ORDER BY taken_at`, d.ID, days)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	pts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (point, error) {
		var p point
		return p, row.Scan(&p.TakenAt, &p.Metrics)
	})
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	// Thin evenly if there are more points than a chart can show; keep the latest.
	if n := len(pts); n > maxHistoryPoints {
		step := float64(n-1) / float64(maxHistoryPoints-1)
		thin := make([]point, 0, maxHistoryPoints)
		for i := 0; i < maxHistoryPoints; i++ {
			thin = append(thin, pts[int(float64(i)*step+0.5)])
		}
		pts = thin
	}
	if pts == nil {
		pts = []point{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "points": pts})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	d, ok := s.orgDevice(w, r)
	if !ok {
		return
	}
	if _, err := s.DB.Exec(r.Context(), `DELETE FROM devices WHERE id = $1`, d.ID); err != nil {
		s.internalErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type alertJSON struct {
	ID         string     `json:"id"`
	DeviceID   string     `json:"device_id"`
	Hostname   string     `json:"hostname"`
	RuleID     string     `json:"rule_id"`
	RuleName   string     `json:"rule_name"`
	Metric     string     `json:"metric"`
	Status     string     `json:"status"`
	Value      *float64   `json:"value"`
	Message    string     `json:"message"`
	OpenedAt   time.Time  `json:"opened_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	NotifiedAt *time.Time `json:"notified_at"`
}

func (s *Server) queryAlerts(r *http.Request, status, deviceID string, limit int) ([]alertJSON, error) {
	rows, err := s.DB.Query(r.Context(), `SELECT a.id, a.device_id, d.hostname, a.rule_id, ru.name, ru.metric, a.status,
			a.value, a.message, a.opened_at, a.resolved_at, a.notified_at
		FROM alerts a JOIN devices d ON d.id = a.device_id JOIN alert_rules ru ON ru.id = a.rule_id
		WHERE a.org_id = $1 AND ($2 = 'all' OR a.status = $2) AND ($3 = '' OR a.device_id::text = $3)
		ORDER BY a.status = 'open' DESC, coalesce(a.resolved_at, a.opened_at) DESC LIMIT $4`,
		userFrom(r).OrgID, status, deviceID, limit)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (alertJSON, error) {
		var a alertJSON
		return a, row.Scan(&a.ID, &a.DeviceID, &a.Hostname, &a.RuleID, &a.RuleName, &a.Metric, &a.Status,
			&a.Value, &a.Message, &a.OpenedAt, &a.ResolvedAt, &a.NotifiedAt)
	})
	if list == nil {
		list = []alertJSON{}
	}
	return list, err
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "":
		status = "open"
	case "open", "resolved", "all":
	default:
		writeErr(w, http.StatusBadRequest, "status must be open, resolved or all")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	list, err := s.queryAlerts(r, status, "", limit)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": list})
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT id, name, metric, op, threshold, enabled FROM alert_rules
		WHERE org_id = $1 ORDER BY created_at, name`, userFrom(r).OrgID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (alerts.Rule, error) {
		var x alerts.Rule
		return x, row.Scan(&x.ID, &x.Name, &x.Metric, &x.Op, &x.Threshold, &x.Enabled)
	})
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if list == nil {
		list = []alerts.Rule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": list, "metrics": alerts.Metrics, "ops": alerts.Ops})
}

type ruleInput struct {
	Name      *string  `json:"name"`
	Metric    *string  `json:"metric"`
	Op        *string  `json:"op"`
	Threshold *float64 `json:"threshold"`
	Enabled   *bool    `json:"enabled"`
}

func (in ruleInput) apply(r *alerts.Rule) {
	if in.Name != nil {
		r.Name = strings.TrimSpace(*in.Name)
	}
	if in.Metric != nil {
		r.Metric = *in.Metric
	}
	if in.Op != nil {
		r.Op = *in.Op
	}
	if in.Threshold != nil {
		r.Threshold = *in.Threshold
	}
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	var in ruleInput
	if !decode(w, r, 8<<10, true, &in) {
		return
	}
	rule := alerts.Rule{Enabled: true}
	in.apply(&rule)
	if in.Threshold == nil {
		writeErr(w, http.StatusBadRequest, "threshold is required")
		return
	}
	if err := rule.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	orgID := userFrom(r).OrgID
	err := s.DB.QueryRow(r.Context(), `INSERT INTO alert_rules (org_id, name, metric, op, threshold, enabled)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, orgID, rule.Name, rule.Metric, rule.Op, rule.Threshold, rule.Enabled).Scan(&rule.ID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.evaluateOrgAsync(orgID)
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in ruleInput
	if !decode(w, r, 8<<10, true, &in) {
		return
	}
	orgID := userFrom(r).OrgID
	var rule alerts.Rule
	err := s.DB.QueryRow(r.Context(), `SELECT id, name, metric, op, threshold, enabled FROM alert_rules WHERE id = $1 AND org_id = $2`, id, orgID).
		Scan(&rule.ID, &rule.Name, &rule.Metric, &rule.Op, &rule.Threshold, &rule.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "rule not found")
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	before := rule
	in.apply(&rule)
	if err := rule.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	err = pgx.BeginFunc(r.Context(), s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE alert_rules SET name = $2, metric = $3, op = $4, threshold = $5, enabled = $6 WHERE id = $1`,
			rule.ID, rule.Name, rule.Metric, rule.Op, rule.Threshold, rule.Enabled); err != nil {
			return err
		}
		// A changed condition starts fresh; open alerts from the old one close.
		if rule.Metric != before.Metric || rule.Op != before.Op || rule.Threshold != before.Threshold {
			_, err := tx.Exec(r.Context(), `UPDATE alerts SET status = 'resolved', resolved_at = now() WHERE rule_id = $1 AND status = 'open'`, rule.ID)
			return err
		}
		return nil
	})
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.evaluateOrgAsync(orgID)
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tag, err := s.DB.Exec(r.Context(), `DELETE FROM alert_rules WHERE id = $1 AND org_id = $2`, id, userFrom(r).OrgID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeErr(w, http.StatusNotFound, "rule not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type orgJSON struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	Plan                  string    `json:"plan"`
	EnrollCode            string    `json:"enroll_code"`
	ReportIntervalMinutes int       `json:"report_interval_minutes"`
	AllowAI               bool      `json:"allow_ai"`
	SlackWebhookURL       string    `json:"slack_webhook_url"`
	CreatedAt             time.Time `json:"created_at"`
	DeviceCount           int       `json:"device_count"`
}

func (s *Server) loadOrg(r *http.Request) (orgJSON, error) {
	var o orgJSON
	err := s.DB.QueryRow(r.Context(), `SELECT id, name, plan, enroll_code, report_interval_minutes, allow_ai, slack_webhook_url, created_at,
			(SELECT count(*) FROM devices WHERE org_id = orgs.id)
		FROM orgs WHERE id = $1`, userFrom(r).OrgID).
		Scan(&o.ID, &o.Name, &o.Plan, &o.EnrollCode, &o.ReportIntervalMinutes, &o.AllowAI, &o.SlackWebhookURL, &o.CreatedAt, &o.DeviceCount)
	if userFrom(r).Role != "admin" {
		o.EnrollCode = ""
		if o.SlackWebhookURL != "" {
			o.SlackWebhookURL = "configured"
		}
	}
	return o, err
}

func (s *Server) getOrg(w http.ResponseWriter, r *http.Request) {
	o, err := s.loadOrg(r)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) updateOrg(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name                  *string `json:"name"`
		ReportIntervalMinutes *int    `json:"report_interval_minutes"`
		AllowAI               *bool   `json:"allow_ai"`
		SlackWebhookURL       *string `json:"slack_webhook_url"`
	}
	if !decode(w, r, 8<<10, true, &in) {
		return
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name cannot be empty")
		return
	}
	if in.ReportIntervalMinutes != nil && (*in.ReportIntervalMinutes < 15 || *in.ReportIntervalMinutes > 1440) {
		writeErr(w, http.StatusBadRequest, "report interval must be between 15 and 1440 minutes")
		return
	}
	if in.SlackWebhookURL != nil {
		*in.SlackWebhookURL = strings.TrimSpace(*in.SlackWebhookURL)
		if !alerts.ValidWebhook(*in.SlackWebhookURL) {
			writeErr(w, http.StatusBadRequest, "Slack webhook URLs start with https://hooks.slack.com/")
			return
		}
	}
	var name *string
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		name = &n
	}
	_, err := s.DB.Exec(r.Context(), `UPDATE orgs SET
			name = coalesce($2, name),
			report_interval_minutes = coalesce($3, report_interval_minutes),
			allow_ai = coalesce($4, allow_ai),
			slack_webhook_url = coalesce($5, slack_webhook_url)
		WHERE id = $1`, userFrom(r).OrgID, name, in.ReportIntervalMinutes, in.AllowAI, in.SlackWebhookURL)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.getOrg(w, r)
}

func (s *Server) rotateCode(w http.ResponseWriter, r *http.Request) {
	if _, err := s.DB.Exec(r.Context(), `UPDATE orgs SET enroll_code = $2 WHERE id = $1`, userFrom(r).OrgID, NewEnrollCode()); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.getOrg(w, r)
}

func (s *Server) testSlack(w http.ResponseWriter, r *http.Request) {
	o, err := s.loadOrg(r)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if o.SlackWebhookURL == "" {
		writeErr(w, http.StatusBadRequest, "no Slack webhook is configured")
		return
	}
	if err := s.Alerts.SendSlack(r.Context(), o.SlackWebhookURL, ":wave: Tidyfleet alerts for *"+o.Name+"* will be posted here."); err != nil {
		writeErr(w, http.StatusBadGateway, "Slack did not accept the message: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}
