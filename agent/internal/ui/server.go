// Package ui serves the cleaner's local web app on 127.0.0.1. Nothing it
// shows leaves the machine. Every API call needs the random token from the
// launch URL, and the Host header must be the loopback address, so other
// websites (including through DNS rebinding) cannot read or drive it.
package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/reporter"
	"github.com/tidyfleet/tidyfleet/agent/internal/rules"
	"github.com/tidyfleet/tidyfleet/agent/internal/scanner"
	"github.com/tidyfleet/tidyfleet/agent/internal/tree"
)

// static holds the Next.js export from cleaner-ui/ (built by `make cleaner-ui`).
// "all:" keeps Next's _next/ folder, which a plain pattern would skip.
//
//go:embed all:static
var staticFS embed.FS

type Server struct {
	Version string

	token string
	host  string          // expected Host header, e.g. 127.0.0.1:51234
	ctx   context.Context // lives as long as the server; background scans use it

	// scanMu serializes scans and cleanups; mu guards the fields below.
	scanMu   sync.Mutex
	mu       sync.RWMutex
	tree     *tree.Tree
	report   *scanner.Report
	scanning bool
	scanErr  string
	visited  atomic.Int64
	lastPing atomic.Int64
	// recentMoves are folders the user moved in this session, so a move can
	// be undone even when the new place is outside the scanned folders.
	recentMoves map[string]bool
}

// Listen binds a loopback port (0 picks a free one) and returns the URL to open.
func (s *Server) Listen(port int) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, "", err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return nil, "", err
	}
	s.token = hex.EncodeToString(b)
	s.host = ln.Addr().String()
	return ln, fmt.Sprintf("http://%s/#token=%s", s.host, s.token), nil
}

// Serve runs until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.ctx = ctx
	hs := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	s.startScan(ctx, "")
	err := hs.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Handler() http.Handler {
	if s.ctx == nil {
		s.ctx = context.Background()
	}
	if s.recentMoves == nil {
		s.recentMoves = map[string]bool{}
	}
	static, _ := fs.Sub(staticFS, "static")
	mux := http.NewServeMux()
	if _, err := fs.Stat(static, "index.html"); err == nil {
		mux.Handle("GET /", http.FileServerFS(static))
	} else {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "The cleaner UI is not built into this binary. Run `make cleaner-ui agent-darwin`.", http.StatusNotFound)
		})
	}
	api := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.guard(h)) }
	api("GET /api/state", s.state)
	api("POST /api/scan", s.scan)
	api("GET /api/tree", s.treeLevel)
	api("POST /api/preview", s.preview)
	api("POST /api/clean", s.clean)
	api("POST /api/reveal", s.reveal)
	api("POST /api/exclude", s.exclude)
	api("GET /api/config", s.getConfig)
	api("POST /api/config", s.setConfig)
	api("GET /api/browse", s.browse)
	api("POST /api/choose-folder", s.chooseFolder)
	api("POST /api/folders", s.folderList)
	api("POST /api/manage/preview", s.managePreview)
	api("POST /api/manage", s.manage)
	api("GET /api/rules", s.listRules)
	api("GET /api/log", s.log)
	api("GET /api/health", s.health)
	api("GET /api/sharing", s.sharing)
	api("POST /api/enroll", s.enroll)
	api("POST /api/leave", s.leave)
	api("POST /api/report", s.sendReport)
	return s.hostCheck(mux)
}

// hostCheck blocks DNS-rebinding: only the exact loopback host is served.
func (s *Server) hostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.host != "" && r.Host != s.host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		// Next's static export bootstraps with inline scripts; nothing loads from elsewhere.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// guard requires the launch token in a custom header. Browsers cannot add
// custom headers cross-origin without a CORS preflight, which is never granted.
func (s *Server) guard(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Tidyfleet-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeErr(w, http.StatusUnauthorized, "missing or wrong token; reopen the link printed by `tidyfleet ui`")
			return
		}
		s.lastPing.Store(time.Now().Unix())
		w.Header().Set("Cache-Control", "no-store")
		h(w, r)
	})
}

// LastActivity is when the page last called the API (for idle shutdown).
func (s *Server) LastActivity() time.Time { return time.Unix(s.lastPing.Load(), 0) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return false
	}
	return true
}

func loadRules() ([]rules.Rule, error) {
	dir, err := config.Path("rules")
	if err != nil {
		return nil, err
	}
	return rules.Load(dir)
}

// startScan measures the tree and runs the rule scan in the background. With
// path set and a tree present, only that folder is re-measured.
func (s *Server) startScan(ctx context.Context, path string) bool {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return false
	}
	s.scanning, s.scanErr = true, ""
	s.visited.Store(0)
	t := s.tree
	s.mu.Unlock()

	go func() {
		s.scanMu.Lock()
		defer s.scanMu.Unlock()
		err := s.runScan(ctx, t, path)
		s.mu.Lock()
		s.scanning = false
		if err != nil {
			s.scanErr = err.Error()
		}
		s.mu.Unlock()
	}()
	return true
}

func (s *Server) runScan(ctx context.Context, t *tree.Tree, path string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	all, err := loadRules()
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	var rep scanner.Report
	wg.Add(1)
	go func() { defer wg.Done(); rep = scanner.Scan(ctx, cfg, all) }()
	if path != "" && t != nil {
		t.Rescan(ctx, path)
	} else {
		t = tree.Build(ctx, cfg.ScanRoots, tree.Options{Exclude: cfg.Exclude, Visited: &s.visited})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_ = reporter.SaveLastScan(rep)
	s.mu.Lock()
	s.tree, s.report = t, &rep
	s.mu.Unlock()
	return nil
}

func (s *Server) snapshot() (*tree.Tree, *scanner.Report) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tree, s.report
}
