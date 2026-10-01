package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/cleaner"
	"github.com/tidyfleet/tidyfleet/agent/internal/config"
)

func TestManageTrashAndMove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("TIDYFLEET_HOME", filepath.Join(home, "tf"))
	root := filepath.Join(home, "code")
	for _, d := range []string{"wip/src", "old/docs", "keep/lib", "archive"} {
		must(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	must(t, os.WriteFile(filepath.Join(root, "wip", "src", "main.go"), []byte("package main"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "old", "docs", "a.txt"), make([]byte, 5000), 0o644))
	git(t, filepath.Join(root, "wip"), "init", "-q")
	must(t, config.Save(config.Config{ScanRoots: []string{root}, DeleteMode: config.DeleteTrash, MaxDepth: 8,
		DisabledRules: []string{"docker", "go-modcache", "gradle-cache", "ios-simulators", "npm-cache", "pip-cache", "pnpm-store", "xcode-deriveddata", "yarn-cache"}}))

	s := &Server{Version: "test"}
	ln, _, err := s.Listen(0)
	must(t, err)
	ts := httptest.NewUnstartedServer(s.Handler())
	ts.Listener.Close()
	ts.Listener = ln
	ts.Start()
	defer ts.Close()
	call := func(path string, body any, out any) int {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+path, bytes.NewReader(b))
		req.Header.Set("X-Tidyfleet-Token", s.token)
		resp, err := http.DefaultClient.Do(req)
		must(t, err)
		defer resp.Body.Close()
		if out != nil {
			json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}
	s.startScan(s.ctx, "")
	for i := 0; ; i++ {
		if tr, _ := s.snapshot(); tr != nil {
			s.mu.RLock()
			scanning := s.scanning
			s.mu.RUnlock()
			if !scanning {
				break
			}
		}
		if i > 100 {
			t.Fatal("scan did not finish")
		}
		time.Sleep(50 * time.Millisecond)
	}

	type preview struct {
		Items []manageItem
	}
	blocked := func(action, dest string, paths ...string) []string {
		var p preview
		call("/api/manage/preview", manageRequest{Action: action, Paths: paths, Dest: dest}, &p)
		out := []string{}
		for _, it := range p.Items {
			out = append(out, it.Blocked)
		}
		return out
	}

	// Guardrails.
	for name, path := range map[string]string{
		"scan location": root, "home folder": home, "system folder": "/usr", "outside the tree": "/tmp",
		"contains a scan location": filepath.Dir(root),
	} {
		if b := blocked("trash", "", path); len(b) != 1 || b[0] == "" {
			t.Errorf("%s (%s) should be blocked: %v", name, path, b)
		}
	}
	if b := blocked("trash", "", filepath.Join(root, "old"), filepath.Join(root, "old", "docs")); b[0] != "" || !strings.HasPrefix(b[1], "already included") {
		t.Errorf("nested selection: %v", b)
	}

	// Warnings: a repo with uncommitted work.
	var p preview
	call("/api/manage/preview", manageRequest{Action: "trash", Paths: []string{filepath.Join(root, "wip")}}, &p)
	w := strings.Join(p.Items[0].Warnings, "|")
	if !strings.Contains(w, "git repository") || !strings.Contains(w, "uncommitted") || !strings.Contains(w, "Changed today") {
		t.Errorf("warnings: %v", p.Items[0].Warnings)
	}

	// Move errors come back from the preview.
	if st := call("/api/manage/preview", manageRequest{Action: "move", Paths: []string{filepath.Join(root, "old")}, Dest: filepath.Join(root, "old", "docs")}, nil); st != 400 {
		t.Errorf("move into itself: %d", st)
	}
	must(t, os.MkdirAll(filepath.Join(root, "archive", "keep"), 0o755))
	if b := blocked("move", filepath.Join(root, "archive"), filepath.Join(root, "keep")); !strings.Contains(b[0], "already exists") {
		t.Errorf("name clash: %v", b)
	}

	// Move, then undo.
	type result struct {
		Results []manageResult
	}
	var r result
	call("/api/manage", manageRequest{Action: "move", Paths: []string{filepath.Join(root, "old")}, Dest: filepath.Join(root, "archive")}, &r)
	moved := filepath.Join(root, "archive", "old")
	if len(r.Results) != 1 || !r.Results[0].OK || r.Results[0].To != moved {
		t.Fatalf("move: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(moved, "docs", "a.txt")); err != nil {
		t.Fatal("moved folder missing its files")
	}
	tr, _ := s.snapshot()
	if !tr.Contains(moved) || tr.Contains(filepath.Join(root, "old")) {
		t.Error("tree not updated after move")
	}
	call("/api/manage", manageRequest{Action: "move", Paths: []string{moved}, Dest: root}, &r)
	if !r.Results[0].OK {
		t.Fatalf("undo: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, "old", "docs", "a.txt")); err != nil {
		t.Fatal("undo did not put the folder back")
	}

	// Trash goes to the Trash, never permanent, even with permanent mode on.
	cfg, _ := config.Load()
	cfg.DeleteMode = config.DeletePermanent
	must(t, config.Save(cfg))
	call("/api/manage", manageRequest{Action: "trash", Paths: []string{filepath.Join(root, "old")}}, &r)
	if !r.Results[0].OK {
		t.Fatalf("trash: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "Trash", "files", "old", "docs", "a.txt")); err != nil {
		t.Errorf("trashed folder should be recoverable from the Trash: %v", err)
	}
	// Blocked items are reported, not acted on.
	call("/api/manage", manageRequest{Action: "trash", Paths: []string{root}}, &r)
	if r.Results[0].OK || r.Results[0].Error == "" {
		t.Errorf("scan root was trashed: %+v", r)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("scan root must still exist")
	}

	logged, _ := cleaner.ReadLog()
	methods := []string{}
	for _, e := range logged {
		methods = append(methods, e.Method)
	}
	if got := strings.Join(methods, ","); got != "move,move,trash (by hand)" {
		t.Errorf("log methods: %s", got)
	}
}
