package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/cleaner"
	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/fsutil"
	"github.com/tidyfleet/tidyfleet/agent/internal/scanner"
)

// Manual folder actions: the user picks any folder and moves it to the Trash
// or to another folder. Unlike cleanup these are not limited to regenerable
// output, so they are guarded differently: always recoverable (Trash, never
// permanent), never on protected or scan-location folders, always confirmed
// in the UI after a preview with warnings, and always logged.

// protected returns folders that must never be trashed or moved, and whose
// ancestors must not be either.
func protected() []string {
	home, _ := os.UserHomeDir()
	out := []string{"/", "/System", "/Applications", "/Library", "/Users", "/usr", "/bin", "/sbin", "/etc", "/var", "/private", "/opt", "/Volumes", "/home", "/root"}
	if v := os.Getenv("SystemDrive"); v != "" {
		out = append(out, v+`\`, v+`\Windows`, v+`\Program Files`, v+`\Program Files (x86)`, v+`\Users`)
	}
	if home != "" {
		out = append(out, home)
		for _, n := range []string{"Desktop", "Documents", "Downloads", "Library", "Pictures", "Music", "Movies", "Public", "Applications", ".ssh", ".gnupg", ".config", ".local", "AppData"} {
			out = append(out, filepath.Join(home, n))
		}
	}
	if d, err := config.Dir(); err == nil {
		out = append(out, d)
	}
	return out
}

// sourceProblem explains why a folder may not be trashed or moved, or "".
func (s *Server) sourceProblem(p string, cfg config.Config) string {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return "not a full folder path"
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return "no longer exists"
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "is a link, not a folder"
	}
	if !fi.IsDir() {
		return "is not a folder"
	}
	for _, x := range protected() {
		if p == filepath.Clean(x) {
			return "is a protected system or home folder"
		}
	}
	for _, root := range cfg.ScanRoots {
		root = filepath.Clean(root)
		if p == root {
			return "is a scan location (remove it in Settings instead)"
		}
		if scanner.IsUnder(root, []string{p}) {
			return "contains a scan location"
		}
	}
	if scanner.IsUnder(p, cfg.Exclude) {
		return "is in your never-touch list"
	}
	for _, x := range protected() {
		if x != "/" && scanner.IsUnder(filepath.Clean(x), []string{p}) {
			return "contains a protected folder"
		}
	}
	// Only folders the user can see in the tree, or that they just moved.
	t, _ := s.snapshot()
	s.mu.RLock()
	recent := s.recentMoves[p]
	s.mu.RUnlock()
	if !(t != nil && t.Contains(p)) && !recent {
		return "is not in the scanned folders"
	}
	return ""
}

type manageItem struct {
	Path     string   `json:"path"`
	Name     string   `json:"name"`
	Size     int64    `json:"size"`
	Warnings []string `json:"warnings"`
	Blocked  string   `json:"blocked,omitempty"`
	Target   string   `json:"target,omitempty"` // where a move would put it
}

type manageRequest struct {
	Action string   `json:"action"` // "trash" or "move"
	Paths  []string `json:"paths"`
	Dest   string   `json:"dest"`
}

// warnings flags things worth a second look before a folder goes away.
func warnings(ctx context.Context, p string) []string {
	out := []string{}
	repos, dirty := 0, 0
	var newest time.Time
	seen := 0
	_ = filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return nil
		}
		if seen++; seen > 20000 {
			return fs.SkipAll
		}
		if d.IsDir() && d.Name() == ".git" {
			repos++
			if isDirty(ctx, filepath.Dir(q)) {
				dirty++
			}
			return fs.SkipDir
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "target" || d.Name() == ".next") {
			return fs.SkipDir
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if repos == 1 {
		out = append(out, "Contains a git repository")
	} else if repos > 1 {
		out = append(out, fmt.Sprintf("Contains %d git repositories", repos))
	}
	if dirty > 0 {
		out = append(out, "Has uncommitted changes that are not saved anywhere else")
	}
	if !newest.IsZero() {
		if days := int(time.Since(newest).Hours() / 24); days < 14 {
			out = append(out, fmt.Sprintf("Changed %s", map[bool]string{true: "today", false: fmt.Sprintf("%d days ago", days)}[days == 0]))
		}
	}
	return out
}

func gitCmd(ctx context.Context, repo string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	return cmd
}

func isDirty(ctx context.Context, repo string) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := gitCmd(ctx, repo, "status", "--porcelain").Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// destProblem explains why a folder cannot be the destination of a move.
func destProblem(dest string, sources []string) string {
	if !filepath.IsAbs(dest) {
		return "choose a full folder path"
	}
	fi, err := os.Stat(dest)
	if err != nil || !fi.IsDir() {
		return "destination folder doesn't exist"
	}
	for _, src := range sources {
		if scanner.IsUnder(dest, []string{src}) {
			return "can't move a folder into itself"
		}
	}
	if d, err := config.Dir(); err == nil && scanner.IsUnder(dest, []string{d}) {
		return "can't move folders into Tidyfleet's data folder"
	}
	return ""
}

func (s *Server) prepare(ctx context.Context, req manageRequest) ([]manageItem, string, error) {
	if req.Action != "trash" && req.Action != "move" {
		return nil, "", errors.New("action must be trash or move")
	}
	if len(req.Paths) == 0 || len(req.Paths) > 200 {
		return nil, "", errors.New("select between 1 and 200 folders")
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, "", err
	}
	dest := ""
	var clean []string
	for _, p := range req.Paths {
		clean = append(clean, filepath.Clean(p))
	}
	if req.Action == "move" {
		dest = filepath.Clean(req.Dest)
		if msg := destProblem(dest, clean); msg != "" {
			return nil, "", errors.New(msg)
		}
	}
	t, _ := s.snapshot()
	items := make([]manageItem, 0, len(clean))
	seen := map[string]bool{}
	for _, p := range clean {
		if seen[p] {
			continue
		}
		seen[p] = true
		it := manageItem{Path: p, Name: filepath.Base(p), Warnings: []string{}}
		if t != nil {
			it.Size, _ = t.SizeOf(p)
		}
		// A folder inside another selected folder goes with its parent.
		for _, q := range clean {
			if q != p && scanner.IsUnder(p, []string{q}) {
				it.Blocked = "already included with " + filepath.Base(q)
			}
		}
		if it.Blocked == "" {
			it.Blocked = s.sourceProblem(p, cfg)
		}
		if it.Blocked == "" && req.Action == "move" {
			it.Target = filepath.Join(dest, it.Name)
			switch {
			case filepath.Dir(p) == dest:
				it.Blocked = "is already in that folder"
			default:
				if _, err := os.Lstat(it.Target); err == nil {
					it.Blocked = "a folder named " + it.Name + " already exists there"
				}
			}
		}
		if it.Blocked == "" {
			it.Warnings = warnings(ctx, p)
		}
		items = append(items, it)
	}
	return items, dest, nil
}

func (s *Server) managePreview(w http.ResponseWriter, r *http.Request) {
	var req manageRequest
	if !decode(w, r, &req) {
		return
	}
	items, dest, err := s.prepare(r.Context(), req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": req.Action, "dest": dest, "items": items})
}

type manageResult struct {
	Path  string `json:"path"`
	To    string `json:"to,omitempty"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Size  int64  `json:"size"`
}

// manage performs a confirmed trash or move. Every check from the preview
// runs again first, because things may have changed in between.
func (s *Server) manage(w http.ResponseWriter, r *http.Request) {
	var req manageRequest
	if !decode(w, r, &req) {
		return
	}
	if !s.scanMu.TryLock() {
		writeErr(w, http.StatusConflict, "a scan is running; try again when it finishes")
		return
	}
	defer s.scanMu.Unlock()
	items, dest, err := s.prepare(r.Context(), req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	t, _ := s.snapshot()
	results := []manageResult{}
	var done []string
	for _, it := range items {
		if it.Blocked != "" {
			if !strings.HasPrefix(it.Blocked, "already included") {
				results = append(results, manageResult{Path: it.Path, Error: it.Blocked, Size: it.Size})
			}
			continue
		}
		res := manageResult{Path: it.Path, Size: it.Size}
		entry := cleaner.LogEntry{Time: time.Now(), RuleID: "manual", Path: it.Path, Bytes: it.Size}
		if req.Action == "trash" {
			entry.Method = "trash (by hand)"
			res.To, err = fsutil.MoveToTrash(it.Path)
			entry.TrashedTo = res.To
		} else {
			entry.Method = "move"
			err = os.Rename(it.Path, it.Target)
			if errors.Is(err, syscall.EXDEV) {
				err = errors.New("the destination is on a different disk; moving between disks isn't supported yet")
			}
			res.To, entry.TrashedTo = it.Target, it.Target
		}
		if err != nil {
			res.Error, entry.Error = err.Error(), err.Error()
		} else {
			res.OK, entry.OK = true, true
			done = append(done, it.Path)
			if req.Action == "move" {
				s.mu.Lock()
				s.recentMoves[it.Target] = true // so the move can be undone
				s.mu.Unlock()
			}
		}
		_ = cleaner.Record(entry)
		results = append(results, res)
	}

	// Keep the cached tree and cleanup report in step.
	if t != nil {
		for _, p := range done {
			t.Remove(p)
		}
		if req.Action == "move" && t.Contains(dest) {
			t.Rescan(r.Context(), dest)
		}
	}
	if _, rep := s.snapshot(); rep != nil && len(done) > 0 {
		s.mu.Lock()
		r2 := *rep
		r2.Items = nil
		for _, it := range rep.Items {
			if it.Path == "" || !scanner.IsUnder(it.Path, done) {
				r2.Items = append(r2.Items, it)
			}
		}
		r2.ReclaimableBytes, r2.SkippedBytes, _ = totals(&r2)
		s.report = &r2
		s.mu.Unlock()
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": req.Action, "dest": dest, "results": results})
}
