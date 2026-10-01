package ui

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/config"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestUIFlow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("TIDYFLEET_HOME", filepath.Join(home, "tf"))
	root := filepath.Join(home, "Projects")
	app := filepath.Join(root, "old-app")
	must(t, os.MkdirAll(filepath.Join(app, "node_modules", "dep"), 0o755))
	must(t, os.WriteFile(filepath.Join(app, "package.json"), []byte("{}"), 0o644))
	must(t, os.WriteFile(filepath.Join(app, ".gitignore"), []byte("node_modules/\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(app, "node_modules", "dep", "index.js"), make([]byte, 50000), 0o644))
	must(t, os.MkdirAll(filepath.Join(root, "notes"), 0o755))
	git(t, app, "init", "-q")
	git(t, app, "add", "package.json", ".gitignore")
	git(t, app, "commit", "-qm", "init")
	old := time.Now().Add(-200 * 24 * time.Hour)
	filepath.WalkDir(app, func(p string, _ fs.DirEntry, err error) error {
		if err == nil {
			os.Chtimes(p, old, old)
		}
		return nil
	})
	// Only project rules, so the test never touches real caches or tools.
	must(t, config.Save(config.Config{
		ScanRoots: []string{root}, StaleDays: 60, DeleteMode: config.DeleteTrash, MaxDepth: 8,
		DisabledRules: []string{"docker", "go-modcache", "gradle-cache", "ios-simulators", "npm-cache", "pip-cache", "pnpm-store", "xcode-deriveddata", "yarn-cache"},
	}))

	s := &Server{Version: "test"}
	ln, url, err := s.Listen(0)
	must(t, err)
	ts := httptest.NewUnstartedServer(s.Handler())
	ts.Listener.Close()
	ts.Listener = ln
	ts.Start()
	defer ts.Close()
	if !strings.Contains(url, "#token="+s.token) {
		t.Fatalf("launch url %q lacks token", url)
	}

	call := func(method, path string, body any, out any) int {
		t.Helper()
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
		req.Header.Set("X-Tidyfleet-Token", s.token)
		resp, err := http.DefaultClient.Do(req)
		must(t, err)
		defer resp.Body.Close()
		if out != nil {
			json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}

	// Security: token required; a foreign Host is rejected (DNS rebinding).
	resp, _ := http.Get(ts.URL + "/api/state")
	if resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/state", nil)
	req.Host = "evil.example:80"
	req.Header.Set("X-Tidyfleet-Token", s.token)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("foreign host: %d", resp.StatusCode)
	}
	if resp, _ = http.Get(ts.URL + "/"); resp.StatusCode != 200 {
		t.Fatalf("app page: %d", resp.StatusCode)
	}

	// Scan, then wait for it to finish.
	if st := call("POST", "/api/scan", map[string]string{}, nil); st != 202 {
		t.Fatalf("scan: %d", st)
	}
	var state map[string]any
	for i := 0; ; i++ {
		call("GET", "/api/state", nil, &state)
		if state["scanning"] == false && state["roots"] != nil {
			break
		}
		if i > 100 {
			t.Fatal("scan did not finish")
		}
		time.Sleep(100 * time.Millisecond)
	}

	type ent struct {
		Name, Path string
		Size       int64
		Collapsed  bool
		Item       *struct {
			ID       string
			Eligible bool
		}
	}
	var lvl struct{ Entries []ent }
	call("GET", "/api/tree?path="+root, nil, &lvl)
	if len(lvl.Entries) != 2 || lvl.Entries[0].Name != "old-app" {
		t.Fatalf("root level: %+v", lvl.Entries)
	}
	call("GET", "/api/tree?path="+app, nil, &lvl)
	var nm *ent
	for i := range lvl.Entries {
		if lvl.Entries[i].Name == "node_modules" {
			nm = &lvl.Entries[i]
		}
	}
	if nm == nil || !nm.Collapsed || nm.Item == nil || !nm.Item.Eligible {
		t.Fatalf("node_modules should be collapsed and eligible: %+v", nm)
	}
	if st := call("GET", "/api/tree?path=/etc", nil, nil); st != 404 {
		t.Fatalf("path outside tree: %d", st)
	}
	if st := call("POST", "/api/reveal", map[string]string{"path": "/etc/passwd"}, nil); st != 404 {
		t.Fatalf("reveal outside tree: %d", st)
	}

	// The item list has every match with what cleaning would do.
	var il struct{ Items []itemView }
	call("GET", "/api/items", nil, &il)
	if len(il.Items) != 1 || !il.Items[0].Eligible || il.Items[0].Action != "Moves to Trash" || il.Items[0].Path == "" {
		t.Fatalf("items: %+v", il.Items)
	}

	// The dry run changes nothing.
	var pv struct {
		Items []struct{ ID, Action string }
		Total int64
	}
	call("POST", "/api/preview", map[string]any{"ids": []string{nm.Item.ID, "bogus"}}, &pv)
	if len(pv.Items) != 1 || pv.Items[0].Action != "Moves to Trash" || pv.Total <= 0 {
		t.Fatalf("preview: %+v", pv)
	}
	if _, err := os.Stat(filepath.Join(app, "node_modules")); err != nil {
		t.Fatal("preview must not delete")
	}

	// Clean.
	var res struct {
		Freed   int64
		Failed  int
		Skipped []string
	}
	call("POST", "/api/clean", map[string]any{"ids": []string{nm.Item.ID, "bogus"}}, &res)
	if res.Freed <= 0 || res.Failed != 0 || len(res.Skipped) != 1 {
		t.Fatalf("clean: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(app, "node_modules")); !os.IsNotExist(err) {
		t.Fatal("node_modules should be in the Trash")
	}
	for _, keep := range []string{"package.json", ".git"} {
		if _, err := os.Stat(filepath.Join(app, keep)); err != nil {
			t.Fatalf("%s must be kept", keep)
		}
	}
	call("GET", "/api/tree?path="+app, nil, &lvl)
	for _, e := range lvl.Entries {
		if e.Name == "node_modules" {
			t.Fatal("tree still shows the cleaned folder")
		}
	}

	// Exclude: saved to settings. A scan root cannot be excluded.
	if st := call("POST", "/api/exclude", map[string]string{"path": filepath.Join(root, "notes")}, nil); st != 200 {
		t.Fatalf("exclude: %d", st)
	}
	if st := call("POST", "/api/exclude", map[string]string{"path": root}, nil); st != 400 {
		t.Fatalf("excluding a scan root: %d", st)
	}
	if cfg, _ := config.Load(); len(cfg.Exclude) != 1 {
		t.Fatalf("exclude not saved: %v", cfg.Exclude)
	}

	// Settings round trip.
	if st := call("POST", "/api/config", map[string]string{"key": "stale_days", "value": "abc"}, nil); st != 400 {
		t.Fatalf("bad setting: %d", st)
	}
	if st := call("POST", "/api/config", map[string]string{"key": "stale_days", "value": "30"}, nil); st != 200 {
		t.Fatalf("setting: %d", st)
	}
	var lg struct{ Entries []map[string]any }
	call("GET", "/api/log", nil, &lg)
	if len(lg.Entries) != 1 {
		t.Fatalf("log: %+v", lg)
	}

	// Folder picker: browse lists folders and flags projects and scan roots.
	var br struct {
		Path    string
		Entries []folderEntry
	}
	if st := call("GET", "/api/browse?path="+root, nil, &br); st != 200 {
		t.Fatalf("browse: %d", st)
	}
	for _, e := range br.Entries {
		if e.Name == "old-app" && !e.Project {
			t.Error("old-app should look like a project")
		}
	}
	call("GET", "/api/browse?path="+home, nil, &br)
	foundRoot := false
	for _, e := range br.Entries {
		foundRoot = foundRoot || (e.Name == "Projects" && e.Root)
	}
	if !foundRoot {
		t.Errorf("Projects should be flagged as a scan location: %+v", br.Entries)
	}
	if st := call("GET", "/api/browse?path=relative/dir", nil, nil); st != 400 {
		t.Fatalf("relative browse: %d", st)
	}

	folders := func(action, path string) (int, config.Config) {
		var out struct{ Config config.Config }
		st := call("POST", "/api/folders", map[string]string{"list": "scan_roots", "action": action, "path": path}, &out)
		return st, out.Config
	}
	if st, _ := folders("add", app); st != 400 {
		t.Fatalf("folder inside a scan root should be refused: %d", st)
	}
	if st, _ := folders("add", filepath.Join(home, "missing")); st != 400 {
		t.Fatalf("missing folder: %d", st)
	}
	withComma := filepath.Join(home, "a, b")
	must(t, os.MkdirAll(withComma, 0o755))
	if st, c := folders("add", withComma); st != 200 || len(c.ScanRoots) != 2 || c.ScanRoots[1] != withComma {
		t.Fatalf("path with a comma: %d %v", st, c.ScanRoots)
	}
	if st, c := folders("add", home); st != 200 || len(c.ScanRoots) != 1 || c.ScanRoots[0] != home {
		t.Fatalf("parent should replace children: %d %v", st, c.ScanRoots)
	}
	if st, c := folders("remove", home); st != 200 || len(c.ScanRoots) != 0 {
		t.Fatalf("remove: %d %v", st, c.ScanRoots)
	}
}
