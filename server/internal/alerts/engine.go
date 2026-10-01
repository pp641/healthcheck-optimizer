package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tidyfleet/tidyfleet/server/internal/metrics"
)

type Engine struct {
	DB   *pgxpool.Pool
	Log  *slog.Logger
	HTTP *http.Client
	// LatestOS lets operators declare the newest major OS version per os_name
	// (e.g. "macos": 26). The fleet's own newest version is used otherwise.
	LatestOS map[string]int
	Now      func() time.Time
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

type notification struct {
	alertID string
	text    string
}

// EvaluateDevice re-checks every rule for one device, e.g. after it reports.
func (e *Engine) EvaluateDevice(ctx context.Context, orgID, deviceID string) error {
	return e.evaluateOrg(ctx, orgID, deviceID)
}

// EvaluateOrg re-checks every rule for every device in an organization.
func (e *Engine) EvaluateOrg(ctx context.Context, orgID string) error {
	return e.evaluateOrg(ctx, orgID, "")
}

// Sweep evaluates every device in every organization. It catches devices
// that stopped reporting and applies rule changes to the whole fleet.
func (e *Engine) Sweep(ctx context.Context) error {
	rows, err := e.DB.Query(ctx, `SELECT id FROM orgs`)
	if err != nil {
		return err
	}
	orgIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range orgIDs {
		if err := e.evaluateOrg(ctx, id, ""); err != nil {
			errs = append(errs, fmt.Errorf("org %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// Run sweeps periodically until ctx is cancelled.
func (e *Engine) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := e.Sweep(ctx); err != nil && ctx.Err() == nil {
			e.Log.Error("alert sweep", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type deviceRow struct {
	id, hostname string
	lastSeen     *time.Time
	metrics      *metrics.Metrics
}

func (e *Engine) evaluateOrg(ctx context.Context, orgID, onlyDevice string) error {
	var orgName, webhook string
	if err := e.DB.QueryRow(ctx, `SELECT name, slack_webhook_url FROM orgs WHERE id = $1`, orgID).Scan(&orgName, &webhook); err != nil {
		return err
	}

	rows, err := e.DB.Query(ctx, `SELECT id, name, metric, op, threshold, enabled FROM alert_rules WHERE org_id = $1 AND enabled`, orgID)
	if err != nil {
		return err
	}
	rules, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Rule, error) {
		var x Rule
		err := r.Scan(&x.ID, &x.Name, &x.Metric, &x.Op, &x.Threshold, &x.Enabled)
		return x, err
	})
	if err != nil {
		return err
	}

	rows, err = e.DB.Query(ctx, `SELECT id, hostname, last_seen, last_metrics FROM devices
		WHERE org_id = $1 AND ($2 = '' OR id::text = $2)`, orgID, onlyDevice)
	if err != nil {
		return err
	}
	devices, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (deviceRow, error) {
		var d deviceRow
		err := r.Scan(&d.id, &d.hostname, &d.lastSeen, &d.metrics)
		return d, err
	})
	if err != nil {
		return err
	}

	latest, err := e.LatestOSMajors(ctx, orgID)
	if err != nil {
		return err
	}

	// Open alerts: key device|rule -> alert id.
	rows, err = e.DB.Query(ctx, `SELECT id, device_id, rule_id FROM alerts
		WHERE org_id = $1 AND status = 'open' AND ($2 = '' OR device_id::text = $2)`, orgID, onlyDevice)
	if err != nil {
		return err
	}
	open := map[string]string{}
	var aid, did, rid string
	if _, err := pgx.ForEachRow(rows, []any{&aid, &did, &rid}, func() error {
		open[did+"|"+rid] = aid
		return nil
	}); err != nil {
		return err
	}

	now := e.now()
	enabled := map[string]bool{}
	var notes []notification
	for _, r := range rules {
		enabled[r.ID] = true
	}
	for _, d := range devices {
		st := DeviceState{Metrics: d.metrics}
		if d.lastSeen != nil {
			st.LastSeen = *d.lastSeen
		}
		if d.metrics != nil {
			st.LatestOSMajor = latest[d.metrics.OSName]
		}
		for _, r := range rules {
			v, ok := Value(r.Metric, st, now)
			if !ok {
				continue // unknown value: leave any existing alert as it is
			}
			key := d.id + "|" + r.ID
			firing := Compare(v, r.Op, r.Threshold)
			switch {
			case firing && open[key] == "":
				msg := Describe(r, v)
				var id string
				err := e.DB.QueryRow(ctx, `INSERT INTO alerts (org_id, device_id, rule_id, value, message)
					VALUES ($1, $2, $3, $4, $5) ON CONFLICT (device_id, rule_id) WHERE status = 'open' DO NOTHING
					RETURNING id`, orgID, d.id, r.ID, v, msg).Scan(&id)
				if errors.Is(err, pgx.ErrNoRows) {
					continue // opened concurrently
				}
				if err != nil {
					return err
				}
				notes = append(notes, notification{id, fmt.Sprintf(":red_circle: *%s* on *%s* — %s", orgName, d.hostname, msg)})
			case firing:
				if _, err := e.DB.Exec(ctx, `UPDATE alerts SET value = $2, message = $3 WHERE id = $1`, open[key], v, Describe(r, v)); err != nil {
					return err
				}
			case open[key] != "":
				tag, err := e.DB.Exec(ctx, `UPDATE alerts SET status = 'resolved', resolved_at = now()
					WHERE id = $1 AND status = 'open'`, open[key])
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 1 {
					notes = append(notes, notification{open[key], fmt.Sprintf(":white_check_mark: *%s* on *%s* — resolved: %s", orgName, d.hostname, r.Name)})
				}
			}
		}
	}
	// Alerts whose rule was switched off resolve quietly.
	for key, id := range open {
		if !enabled[strings.SplitN(key, "|", 2)[1]] {
			if _, err := e.DB.Exec(ctx, `UPDATE alerts SET status = 'resolved', resolved_at = now() WHERE id = $1`, id); err != nil {
				return err
			}
		}
	}

	if webhook != "" {
		for _, n := range notes {
			if err := e.SendSlack(ctx, webhook, n.text); err != nil {
				e.Log.Warn("slack notification failed", "org", orgID, "err", err)
				continue
			}
			_, _ = e.DB.Exec(ctx, `UPDATE alerts SET notified_at = now() WHERE id = $1`, n.alertID)
		}
	}
	return nil
}

// LatestOSMajors returns the newest major OS version per os_name: the
// newest in the fleet, or the operator's configured version if higher.
func (e *Engine) LatestOSMajors(ctx context.Context, orgID string) (map[string]int, error) {
	rows, err := e.DB.Query(ctx, `SELECT last_metrics->>'os_name', max((last_metrics->>'os_major')::int)
		FROM devices WHERE org_id = $1 AND last_metrics IS NOT NULL GROUP BY 1`, orgID)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	var name string
	var major int
	if _, err := pgx.ForEachRow(rows, []any{&name, &major}, func() error {
		out[name] = major
		return nil
	}); err != nil {
		return nil, err
	}
	for k, v := range e.LatestOS {
		if v > out[k] {
			out[k] = v
		}
	}
	return out, nil
}

// ValidWebhook limits outgoing requests to Slack's webhook host.
func ValidWebhook(url string) bool {
	return url == "" || strings.HasPrefix(url, "https://hooks.slack.com/")
}

func (e *Engine) SendSlack(ctx context.Context, webhook, text string) error {
	if !ValidWebhook(webhook) || webhook == "" {
		return errors.New("not a Slack webhook URL")
	}
	body, _ := json.Marshal(map[string]string{"text": text})
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := e.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack returned HTTP %d", resp.StatusCode)
	}
	return nil
}
