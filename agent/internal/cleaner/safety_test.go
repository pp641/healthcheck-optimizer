package cleaner

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/rules"
	"github.com/tidyfleet/tidyfleet/agent/internal/scanner"
)

// These tests run the real scanner and cleaner against throwaway projects to
// pin down the safety rules: only stale, regenerable, gitignored output in
// clean repos is ever eligible, and nothing else is touched.

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// age sets every file and folder under root (including .git) to be old.
func age(t *testing.T, root string, d time.Duration) {
	old := time.Now().Add(-d)
	filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err == nil {
			os.Chtimes(p, old, old)
		}
		return nil
	})
}

func newRepo(t *testing.T, dir string, files map[string]string, commit ...string) {
	t.Helper()
	for name, body := range files {
		write(t, filepath.Join(dir, name), body)
	}
	git(t, dir, "init", "-q")
	git(t, dir, append([]string{"add", "--"}, commit...)...)
	git(t, dir, "commit", "-q", "-m", "init")
}

func TestScanAndCleanSafety(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("TIDYFLEET_HOME", filepath.Join(home, "tf"))
	root := filepath.Join(home, "Projects")
	const idle = 120 * 24 * time.Hour

	// 1. Stale, clean repo with gitignored node_modules and dist: eligible.
	newRepo(t, filepath.Join(root, "old-app"), map[string]string{
		"package.json": "{}", ".gitignore": "node_modules/\ndist/\n",
		"node_modules/left-pad/index.js": "x", "dist/app.js": "x",
	}, "package.json", ".gitignore")
	// 2. Stale repo with uncommitted changes: skipped by default.
	newRepo(t, filepath.Join(root, "dirty-app"), map[string]string{
		"package.json": "{}", ".gitignore": "node_modules/\n", "node_modules/a/index.js": "x",
	}, "package.json", ".gitignore")
	write(t, filepath.Join(root, "dirty-app", "wip.js"), "unsaved work")
	// 3. dist/ committed to git: never eligible.
	newRepo(t, filepath.Join(root, "committed-dist"), map[string]string{
		"package.json": "{}", "dist/lib.js": "x",
	}, "package.json", "dist/lib.js")
	// 4. dist/ outside any git repo: cannot prove it is build output.
	write(t, filepath.Join(root, "no-git", "package.json"), "{}")
	write(t, filepath.Join(root, "no-git", "dist", "out.js"), "x")
	// 5. Excluded folder.
	newRepo(t, filepath.Join(root, "excluded", "app"), map[string]string{
		"package.json": "{}", ".gitignore": "node_modules/\n", "node_modules/b/index.js": "x",
	}, "package.json", ".gitignore")
	age(t, root, idle)

	// 6. Recently active Rust project: too fresh to clean.
	write(t, filepath.Join(root, "fresh-rs", "Cargo.toml"), "[package]")
	write(t, filepath.Join(root, "fresh-rs", "target", "debug", "bin"), "x")

	cfg := config.Config{
		ScanRoots: []string{root}, Exclude: []string{filepath.Join(root, "excluded")},
		StaleDays: 60, DeleteMode: config.DeleteTrash, MaxDepth: 8,
	}
	all, err := rules.Load("")
	if err != nil {
		t.Fatal(err)
	}
	var enabled []rules.Rule
	for _, r := range all {
		if r.Kind == rules.KindProject { // keep the test off real caches and tools
			enabled = append(enabled, r)
		}
	}
	rep := scanner.Scan(context.Background(), cfg, enabled)

	got := map[string]scanner.Item{}
	for _, it := range rep.Items {
		rel, _ := filepath.Rel(root, it.Path)
		got[rel] = it
	}
	want := map[string]string{ // path -> "" if eligible, else expected skip reason
		"old-app/node_modules":   "",
		"old-app/dist":           "",
		"dirty-app/node_modules": "repo has uncommitted changes",
		"committed-dist/dist":    "contains files tracked by git",
		"no-git/dist":            "not in a git repo, so cannot confirm it is build output",
		"fresh-rs/target":        "project active 0 days ago (threshold 60)",
	}
	for p, reason := range want {
		it, ok := got[p]
		if !ok {
			t.Errorf("%s: not found by scan", p)
			continue
		}
		if it.Eligible != (reason == "") || it.SkipReason != reason {
			t.Errorf("%s: eligible=%v reason=%q, want reason %q", p, it.Eligible, it.SkipReason, reason)
		}
	}
	if _, ok := got["excluded/app/node_modules"]; ok {
		t.Error("excluded folder was scanned")
	}
	if len(got) != len(want) {
		t.Errorf("scan found %d items, want %d: %v", len(got), len(want), got)
	}

	// Clean everything the scan returned. Only the eligible items may go.
	res, err := Clean(context.Background(), cfg, enabled, rep.Items)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 4 {
		t.Errorf("failed = %d, want 4 (the skipped items)", res.Failed)
	}
	for p, reason := range want {
		_, statErr := os.Stat(filepath.Join(root, p))
		if removed := os.IsNotExist(statErr); removed != (reason == "") {
			t.Errorf("%s: removed=%v, want %v", p, removed, reason == "")
		}
	}
	for _, keep := range []string{"old-app/package.json", "dirty-app/wip.js", "old-app/.git"} {
		if _, err := os.Stat(filepath.Join(root, keep)); err != nil {
			t.Errorf("%s must never be touched: %v", keep, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "Trash", "files", "node_modules")); err != nil {
		t.Errorf("node_modules should be in the Trash: %v", err)
	}
	logged, _ := ReadLog()
	if len(logged) != len(rep.Items) {
		t.Errorf("log has %d entries, want %d", len(logged), len(rep.Items))
	}
}

func TestCheckSafeRefusesTamperedItems(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, "Projects")
	write(t, filepath.Join(root, "app", "package.json"), "{}")
	write(t, filepath.Join(root, "app", "src", "main.js"), "source")
	write(t, filepath.Join(root, "repo", ".git", "HEAD"), "ref")
	all, _ := rules.Load("")
	var nm rules.Rule
	for _, r := range all {
		if r.ID == "node-modules" {
			nm = r
		}
	}
	cfg := config.Config{ScanRoots: []string{root}}
	cases := map[string]string{
		"source folder with wrong name": filepath.Join(root, "app", "src"),
		"scan root itself":              root,
		"git repository":                filepath.Join(root, "repo"),
		"relative path":                 "Projects/app",
		"outside scan roots":            filepath.Join(home, "node_modules"),
	}
	os.MkdirAll(filepath.Join(home, "node_modules"), 0o755)
	for name, p := range cases {
		it := scanner.Item{RuleID: nm.ID, Path: p, Eligible: true, CleanMethod: rules.MethodTrash}
		if err := checkSafe(cfg, nm, it); err == nil {
			t.Errorf("%s (%s): checkSafe allowed it", name, p)
		}
	}
}
