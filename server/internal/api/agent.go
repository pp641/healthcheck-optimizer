package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tidyfleet/tidyfleet/server/internal/metrics"
)

type policy struct {
	ReportIntervalMinutes int  `json:"report_interval_minutes"`
	AllowAI               bool `json:"allow_ai"`
}

type deviceCtxKey struct{}

type deviceAuth struct {
	deviceID, orgID string
}

func (s *Server) deviceAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if !strings.HasPrefix(tok, "tfd_") {
			writeErr(w, http.StatusUnauthorized, "device token required")
			return
		}
		var d deviceAuth
		err := s.DB.QueryRow(r.Context(), `SELECT id, org_id FROM devices WHERE token_hash = $1`, HashToken(tok)).Scan(&d.deviceID, &d.orgID)
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusUnauthorized, "unknown device")
			return
		}
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), deviceCtxKey{}, d)))
	}
}

func deviceFrom(r *http.Request) deviceAuth { return r.Context().Value(deviceCtxKey{}).(deviceAuth) }

type enrollRequest struct {
	Code         string `json:"enroll_code"`
	Hostname     string `json:"hostname"`
	OSName       string `json:"os_name"`
	OSVersion    string `json:"os_version"`
	AgentVersion string `json:"agent_version"`
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	var req enrollRequest
	if !decode(w, r, 16<<10, false, &req) {
		return
	}
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if code == "" {
		writeErr(w, http.StatusBadRequest, "enroll_code is required")
		return
	}
	var orgID, orgName string
	var pol policy
	err := s.DB.QueryRow(r.Context(), `SELECT id, name, report_interval_minutes, allow_ai FROM orgs WHERE enroll_code = $1`, code).
		Scan(&orgID, &orgName, &pol.ReportIntervalMinutes, &pol.AllowAI)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "unknown enrollment code")
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	host := clip(req.Hostname, 255)
	if host == "" {
		host = "unnamed device"
	}
	token, hash := NewToken("tfd_")
	var deviceID string
	err = s.DB.QueryRow(r.Context(), `INSERT INTO devices (org_id, hostname, os_name, os_version, agent_version, token_hash)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		orgID, host, clip(req.OSName, 32), clip(req.OSVersion, 64), clip(req.AgentVersion, 32), hash).Scan(&deviceID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.Log.Info("device enrolled", "org", orgID, "device", deviceID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"device_id": deviceID, "device_token": token, "org_name": orgName, "policy": pol,
	})
}

const maxBatch = 100

func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r)
	var req struct {
		Snapshots []json.RawMessage `json:"snapshots"`
	}
	if !decode(w, r, 2<<20, false, &req) {
		return
	}
	if len(req.Snapshots) == 0 || len(req.Snapshots) > maxBatch {
		writeErr(w, http.StatusBadRequest, "send between 1 and 100 snapshots")
		return
	}
	now := time.Now()
	var valid []metrics.Snapshot
	var rejected []string
	for _, raw := range req.Snapshots {
		var snap metrics.Snapshot
		if err := json.Unmarshal(raw, &snap); err != nil {
			rejected = append(rejected, "invalid snapshot: "+err.Error())
			continue
		}
		if err := snap.Validate(now); err != nil {
			rejected = append(rejected, err.Error())
			continue
		}
		valid = append(valid, snap)
	}

	accepted := 0
	var pol policy
	err := pgx.BeginFunc(r.Context(), s.DB, func(tx pgx.Tx) error {
		var newest *metrics.Snapshot
		for i := range valid {
			snap := &valid[i]
			// Stored from the typed struct: unknown fields are never persisted.
			tag, err := tx.Exec(r.Context(), `INSERT INTO device_snapshots (device_id, taken_at, metrics)
				VALUES ($1, $2, $3) ON CONFLICT (device_id, taken_at) DO NOTHING`, d.deviceID, snap.TakenAt, snap.Metrics)
			if err != nil {
				return err
			}
			accepted += int(tag.RowsAffected())
			if newest == nil || snap.TakenAt.After(newest.TakenAt) {
				newest = snap
			}
		}
		if _, err := tx.Exec(r.Context(), `UPDATE devices SET last_seen = now() WHERE id = $1`, d.deviceID); err != nil {
			return err
		}
		if newest != nil {
			m := newest.Metrics
			if _, err := tx.Exec(r.Context(), `UPDATE devices SET last_metrics = $2, last_snapshot_at = $3,
					os_name = $4, os_version = $5, agent_version = $6
				WHERE id = $1 AND (last_snapshot_at IS NULL OR last_snapshot_at <= $3)`,
				d.deviceID, m, newest.TakenAt, m.OSName, m.OSVersion, m.AgentVersion); err != nil {
				return err
			}
		}
		return tx.QueryRow(r.Context(), `SELECT report_interval_minutes, allow_ai FROM orgs WHERE id = $1`, d.orgID).
			Scan(&pol.ReportIntervalMinutes, &pol.AllowAI)
	})
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if len(valid) > 0 {
		if err := s.Alerts.EvaluateDevice(r.Context(), d.orgID, d.deviceID); err != nil {
			s.Log.Error("alert evaluation", "device", d.deviceID, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": accepted, "rejected": rejected, "policy": pol})
}

// leave deletes the device and, by cascade, its snapshots and alerts.
func (s *Server) leave(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r)
	if _, err := s.DB.Exec(r.Context(), `DELETE FROM devices WHERE id = $1`, d.deviceID); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.Log.Info("device left", "org", d.orgID, "device", d.deviceID)
	w.WriteHeader(http.StatusNoContent)
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n]
	}
	return s
}
