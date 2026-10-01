// Package api serves the agent endpoints (enroll, snapshots, leave) and the
// admin endpoints used by the dashboard.
package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tidyfleet/tidyfleet/server/internal/alerts"
)

type Server struct {
	DB     *pgxpool.Pool
	Alerts *alerts.Engine
	Log    *slog.Logger
	// TrustProxy uses X-Forwarded-For for rate limiting behind a reverse proxy.
	TrustProxy bool

	limiter *limiter
}

func (s *Server) Handler() http.Handler {
	s.limiter = newLimiter(10, 5*time.Minute)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)

	// Agent API
	mux.HandleFunc("POST /api/v1/enroll", s.rateLimited(s.enroll))
	mux.HandleFunc("POST /api/v1/snapshots", s.deviceAuth(s.ingest))
	mux.HandleFunc("POST /api/v1/leave", s.deviceAuth(s.leave))

	// Admin API
	mux.HandleFunc("POST /api/v1/auth/login", s.rateLimited(s.login))
	mux.HandleFunc("POST /api/v1/auth/logout", s.userAuth(s.logout))
	mux.HandleFunc("GET /api/v1/me", s.userAuth(s.me))
	mux.HandleFunc("GET /api/v1/fleet", s.userAuth(s.fleet))
	mux.HandleFunc("GET /api/v1/devices/{id}", s.userAuth(s.device))
	mux.HandleFunc("GET /api/v1/devices/{id}/snapshots", s.userAuth(s.deviceSnapshots))
	mux.HandleFunc("DELETE /api/v1/devices/{id}", s.adminOnly(s.deleteDevice))
	mux.HandleFunc("GET /api/v1/alerts", s.userAuth(s.listAlerts))
	mux.HandleFunc("GET /api/v1/alert-rules", s.userAuth(s.listRules))
	mux.HandleFunc("POST /api/v1/alert-rules", s.adminOnly(s.createRule))
	mux.HandleFunc("PATCH /api/v1/alert-rules/{id}", s.adminOnly(s.updateRule))
	mux.HandleFunc("DELETE /api/v1/alert-rules/{id}", s.adminOnly(s.deleteRule))
	mux.HandleFunc("GET /api/v1/org", s.userAuth(s.getOrg))
	mux.HandleFunc("PATCH /api/v1/org", s.adminOnly(s.updateOrg))
	mux.HandleFunc("POST /api/v1/org/rotate-code", s.adminOnly(s.rotateCode))
	mux.HandleFunc("POST /api/v1/org/test-slack", s.adminOnly(s.testSlack))
	return s.logRequests(mux)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		if r.URL.Path != "/healthz" {
			s.Log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rw.status, "ms", time.Since(start).Milliseconds())
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) internalErr(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("internal error", "path", r.URL.Path, "err", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

// decode reads a size-limited JSON body. Admin endpoints are strict about
// unknown fields; the agent endpoint is lenient for forward compatibility.
func decode(w http.ResponseWriter, r *http.Request, limit int64, strict bool, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else if errors.Is(err, io.EOF) {
			writeErr(w, http.StatusBadRequest, "request body is empty")
		} else {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		}
		return false
	}
	return true
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// pathID returns the {id} path value, or writes 404 if it is not a UUID.
func pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeErr(w, http.StatusNotFound, "not found")
		return "", false
	}
	return id, true
}

// --- tokens ---

// NewToken returns a random bearer token and the hash stored in the database.
func NewToken(prefix string) (token string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	token = prefix + base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// --- rate limiting for unauthenticated endpoints ---

// limiter counts failed attempts (bad codes, wrong passwords) per client, so
// guessing is throttled while a whole office behind one NAT address can still
// enroll at once.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, fails: map[string][]time.Time{}}
}

func (l *limiter) recent(key string, now time.Time) []time.Time {
	kept := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = kept
	return kept
}

func (l *limiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key, time.Now())) >= l.max
}

func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.fails[key] = append(l.recent(key, now), now)
	if len(l.fails) > 10000 { // bound memory under a flood of addresses
		for k := range l.fails {
			l.recent(k, now)
		}
	}
}

func (s *Server) clientIP(r *http.Request) string {
	if s.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) rateLimited(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path + "|" + s.clientIP(r)
		if s.limiter.blocked(key) {
			writeErr(w, http.StatusTooManyRequests, "too many failed attempts; try again in a few minutes")
			return
		}
		rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h(rw, r)
		if rw.status == http.StatusUnauthorized || rw.status == http.StatusNotFound {
			s.limiter.fail(key)
		}
	}
}

// evaluateOrgAsync re-checks alerts after a rule change without blocking the request.
func (s *Server) evaluateOrgAsync(orgID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := s.Alerts.EvaluateOrg(ctx, orgID); err != nil {
			s.Log.Error("alert evaluation", "org", orgID, "err", err)
		}
	}()
}
