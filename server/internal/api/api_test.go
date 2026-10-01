package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tidyfleet/tidyfleet/server/internal/alerts"
	"github.com/tidyfleet/tidyfleet/server/internal/db"
)

// testDB creates a throwaway database. Set TIDYFLEET_TEST_DATABASE_URL to a
// Postgres URL whose user may create databases (`make test` does this).
func testDB(t *testing.T) *pgxpool.Pool {
	base := os.Getenv("TIDYFLEET_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TIDYFLEET_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("tidyfleet_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + name
	pool, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil { // idempotent
		t.Fatal(err)
	}
	return pool
}

type client struct {
	t     *testing.T
	base  string
	token string
}

func (c client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			c.t.Fatalf("%s %s: decoding %s: %v", method, path, data, err)
		}
	}
	return resp.StatusCode
}

func TestEndToEnd(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()

	var slackMu sync.Mutex
	var slackMsgs []string
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]string
		json.NewDecoder(r.Body).Decode(&m)
		slackMu.Lock()
		slackMsgs = append(slackMsgs, m["text"])
		slackMu.Unlock()
	}))
	defer slack.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Requests to hooks.slack.com are routed to the fake Slack server.
	slackURL, _ := url.Parse(slack.URL)
	engine := &alerts.Engine{DB: pool, Log: log, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = slackURL.Scheme, slackURL.Host
		return http.DefaultTransport.RoundTrip(r)
	})}}
	srv := &Server{DB: pool, Alerts: engine, Log: log}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	orgID, code, err := CreateOrg(ctx, pool, "Acme Labs", "admin@acme.test", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := CreateOrg(ctx, pool, "Dup", "ADMIN@acme.test", "correct horse battery"); err == nil {
		t.Fatal("duplicate email (case-insensitive) should fail")
	}
	pool.Exec(ctx, `UPDATE orgs SET slack_webhook_url = $2 WHERE id = $1`, orgID, "https://hooks.slack.com/services/T0/B0/test")

	anon := client{t: t, base: ts.URL}

	// Enrollment
	var bad map[string]string
	if st := anon.do("POST", "/api/v1/enroll", map[string]string{"enroll_code": "TF-NOPE"}, &bad); st != 404 {
		t.Fatalf("bad code: %d", st)
	}
	enroll := func(host string) (client, string) {
		var out map[string]any
		body := map[string]string{"enroll_code": strings.ToLower(code), "hostname": host, "os_name": "macos", "os_version": "15.1"}
		if st := anon.do("POST", "/api/v1/enroll", body, &out); st != 201 {
			t.Fatalf("enroll %s: %d", host, st)
		}
		if out["org_name"] != "Acme Labs" {
			t.Fatalf("enroll response: %v", out)
		}
		return client{t: t, base: ts.URL, token: out["device_token"].(string)}, out["device_id"].(string)
	}
	for i := 0; i < 15; i++ { // an office enrolling at once is not throttled
		c, _ := enroll(fmt.Sprintf("bulk-%d", i))
		c.do("POST", "/api/v1/leave", nil, nil)
	}
	for i := 0; i < 9; i++ {
		anon.do("POST", "/api/v1/enroll", map[string]string{"enroll_code": "TF-GUESS"}, nil)
	}
	if st := anon.do("POST", "/api/v1/enroll", map[string]string{"enroll_code": "TF-GUESS"}, nil); st != 429 {
		t.Fatalf("10th failed guess: %d, want 429", st)
	}
	srv.limiter = newLimiter(10, 5*time.Minute) // reset for the rest of the test
	dev, devID := enroll("prajjwal-mbp")
	dev1, dev1ID := enroll("old-imac")

	// Snapshots: one unhealthy, with a stray field that must not be stored.
	now := time.Now().UTC().Truncate(time.Second)
	snap := func(at time.Time, used float64, major int, extra map[string]any) map[string]any {
		m := map[string]any{
			"disk_total_bytes": 500e9, "disk_free_bytes": (100 - used) * 5e9, "disk_used_pct": used,
			"mem_total_bytes": 16e9, "mem_used_pct": 50, "cpu_load_pct": 10,
			"battery_present": true, "battery_health_pct": 88, "os_name": "macos", "os_version": fmt.Sprintf("%d.0", major),
			"os_major": major, "disk_encrypted": true, "firewall_enabled": true, "reclaimable_bytes": 12e9,
		}
		for k, v := range extra {
			m[k] = v
		}
		return map[string]any{"taken_at": at, "metrics": m}
	}
	batch := map[string]any{"snapshots": []any{
		snap(now.Add(-2*time.Hour), 80, 26, nil),
		snap(now.Add(-1*time.Hour), 95, 26, map[string]any{"largest_folder": "/Users/p/secret-project"}),
		snap(now.Add(48*time.Hour), 50, 26, nil), // future: rejected
	}}
	var ing struct {
		Accepted int
		Rejected []string
		Policy   map[string]any
	}
	if st := dev.do("POST", "/api/v1/snapshots", batch, &ing); st != 200 || ing.Accepted != 2 || len(ing.Rejected) != 1 {
		t.Fatalf("ingest: %d %+v", st, ing)
	}
	if ing.Policy["report_interval_minutes"].(float64) != 60 {
		t.Fatalf("policy: %+v", ing.Policy)
	}
	// Retrying the same batch is idempotent.
	dev.do("POST", "/api/v1/snapshots", batch, &ing)
	if ing.Accepted != 0 {
		t.Fatalf("retry accepted %d, want 0", ing.Accepted)
	}
	var stored string
	pool.QueryRow(ctx, `SELECT string_agg(metrics::text, '') FROM device_snapshots`).Scan(&stored)
	if strings.Contains(stored, "secret-project") || strings.Contains(stored, "largest_folder") {
		t.Fatal("unknown snapshot field was stored")
	}
	// The second device is on an old OS.
	dev1.do("POST", "/api/v1/snapshots", map[string]any{"snapshots": []any{snap(now, 40, 13, nil)}}, &ing)

	if st := (client{t: t, base: ts.URL, token: "tfd_bogus"}).do("POST", "/api/v1/snapshots", batch, nil); st != 401 {
		t.Fatalf("bogus token: %d", st)
	}

	// Admin login
	var login struct{ Token string }
	if st := anon.do("POST", "/api/v1/auth/login", map[string]string{"email": "admin@acme.test", "password": "wrong"}, nil); st != 401 {
		t.Fatalf("wrong password: %d", st)
	}
	if st := anon.do("POST", "/api/v1/auth/login", map[string]string{"email": "Admin@Acme.test", "password": "correct horse battery"}, &login); st != 200 {
		t.Fatalf("login: %d", st)
	}
	admin := client{t: t, base: ts.URL, token: login.Token}
	if st := anon.do("GET", "/api/v1/fleet", nil, nil); st != 401 {
		t.Fatalf("fleet without auth: %d", st)
	}

	// Fleet overview
	var fleet struct {
		Summary fleetSummary
		Devices []deviceJSON
	}
	if st := admin.do("GET", "/api/v1/fleet", nil, &fleet); st != 200 {
		t.Fatalf("fleet: %d", st)
	}
	s := fleet.Summary
	if s.Devices != 2 || s.LowDisk != 1 || s.OutdatedOS != 1 || s.ReclaimableBytes != 24e9 {
		t.Fatalf("summary: %+v", s)
	}
	if s.OpenAlerts != 2 { // disk on prajjwal-mbp, OS on old-imac
		t.Fatalf("open alerts = %d, want 2", s.OpenAlerts)
	}
	slackMu.Lock()
	if len(slackMsgs) != 2 || !strings.Contains(strings.Join(slackMsgs, "\n"), "Disk almost full") {
		t.Fatalf("slack: %q", slackMsgs)
	}
	slackMu.Unlock()

	// Device detail and history
	var detail struct {
		Device deviceJSON
		Alerts []alertJSON
	}
	admin.do("GET", "/api/v1/devices/"+devID, nil, &detail)
	if detail.Device.Hostname != "prajjwal-mbp" || detail.Device.Metrics.DiskUsedPct != 95 || len(detail.Alerts) != 1 {
		t.Fatalf("detail: %+v", detail)
	}
	var hist struct{ Points []point }
	admin.do("GET", "/api/v1/devices/"+devID+"/snapshots?days=7", nil, &hist)
	if len(hist.Points) != 2 || hist.Points[0].Metrics.DiskUsedPct != 80 {
		t.Fatalf("history: %+v", hist)
	}
	if st := admin.do("GET", "/api/v1/devices/not-a-uuid", nil, nil); st != 404 {
		t.Fatalf("bad id: %d", st)
	}

	// Disk recovers: alert resolves and Slack hears about it.
	dev.do("POST", "/api/v1/snapshots", map[string]any{"snapshots": []any{snap(now, 60, 26, nil)}}, &ing)
	var al struct{ Alerts []alertJSON }
	admin.do("GET", "/api/v1/alerts?status=resolved", nil, &al)
	if len(al.Alerts) != 1 || al.Alerts[0].Hostname != "prajjwal-mbp" {
		t.Fatalf("resolved alerts: %+v", al.Alerts)
	}

	// Alert rules: tighten the disk rule so 60% fires.
	var rules struct{ Rules []alerts.Rule }
	admin.do("GET", "/api/v1/alert-rules", nil, &rules)
	var diskRule alerts.Rule
	for _, r := range rules.Rules {
		if r.Metric == "disk_used_pct" {
			diskRule = r
		}
	}
	if st := admin.do("PATCH", "/api/v1/alert-rules/"+diskRule.ID, map[string]any{"threshold": 50}, nil); st != 200 {
		t.Fatalf("patch rule: %d", st)
	}
	if err := engine.EvaluateOrg(ctx, orgID); err != nil {
		t.Fatal(err)
	}
	admin.do("GET", "/api/v1/alerts", nil, &al)
	if len(al.Alerts) != 2 {
		t.Fatalf("open alerts after tightening: %+v", al.Alerts)
	}
	if st := admin.do("POST", "/api/v1/alert-rules", map[string]any{"name": "x", "metric": "file_count", "op": "gt", "threshold": 1}, nil); st != 400 {
		t.Fatalf("invalid metric: %d", st)
	}

	// Org settings
	var org orgJSON
	if st := admin.do("PATCH", "/api/v1/org", map[string]any{"report_interval_minutes": 30, "allow_ai": false}, &org); st != 200 || org.ReportIntervalMinutes != 30 {
		t.Fatalf("patch org: %d %+v", st, org)
	}
	if st := admin.do("PATCH", "/api/v1/org", map[string]any{"slack_webhook_url": "http://169.254.169.254/"}, nil); st != 400 {
		t.Fatalf("SSRF webhook accepted: %d", st)
	}
	dev.do("POST", "/api/v1/snapshots", map[string]any{"snapshots": []any{snap(now.Add(time.Minute), 60, 26, nil)}}, &ing)
	if ing.Policy["allow_ai"] != false || ing.Policy["report_interval_minutes"].(float64) != 30 {
		t.Fatalf("policy not propagated: %+v", ing.Policy)
	}
	old := org.EnrollCode
	admin.do("POST", "/api/v1/org/rotate-code", nil, &org)
	if org.EnrollCode == old || anon.do("POST", "/api/v1/enroll", map[string]string{"enroll_code": old}, nil) != 404 {
		t.Fatal("rotated code should invalidate the old one")
	}

	// Leaving deletes the device and its history.
	if st := dev.do("POST", "/api/v1/leave", nil, nil); st != 204 {
		t.Fatalf("leave: %d", st)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM device_snapshots WHERE device_id = $1`, devID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d snapshots left after leave", n)
	}
	if st := dev.do("POST", "/api/v1/snapshots", batch, nil); st != 401 {
		t.Fatalf("left device can still report: %d", st)
	}

	// Another org cannot see this org's devices.
	_, _, err = CreateOrg(ctx, pool, "Other", "other@other.test", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var otherLogin struct{ Token string }
	anon.do("POST", "/api/v1/auth/login", map[string]string{"email": "other@other.test", "password": "correct horse battery"}, &otherLogin)
	other := client{t: t, base: ts.URL, token: otherLogin.Token}
	if st := other.do("GET", "/api/v1/devices/"+dev1ID, nil, nil); st != 404 {
		t.Fatalf("cross-org device access: %d", st)
	}
	if st := other.do("DELETE", "/api/v1/devices/"+dev1ID, nil, nil); st != 404 {
		t.Fatalf("cross-org delete: %d", st)
	}

	// Logout ends the session.
	admin.do("POST", "/api/v1/auth/logout", nil, nil)
	if st := admin.do("GET", "/api/v1/me", nil, nil); st != 401 {
		t.Fatalf("after logout: %d", st)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
