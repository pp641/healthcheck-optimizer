package ui

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/rules"
	"github.com/tidyfleet/tidyfleet/agent/internal/scanner"
)

// projectMarkers make a folder look like a code project in the picker.
var projectMarkers = []string{".git", "package.json", "Cargo.toml", "go.mod", "pom.xml", "build.gradle", "build.gradle.kts", "pyproject.toml", "requirements.txt", "Gemfile", "composer.json", "Package.swift", "pubspec.yaml"}

type folderEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Hidden   bool   `json:"hidden"`
	Project  bool   `json:"project"`
	Root     bool   `json:"root"`     // already a scan location
	Covered  bool   `json:"covered"`  // inside a scan location, so already scanned
	Excluded bool   `json:"excluded"` // in the never-touch list
}

// browse lists the sub-folders of a folder so the picker can navigate. It
// only returns names; nothing is read inside files.
func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	p := r.URL.Query().Get("path")
	if p == "" {
		p = home
	}
	p = filepath.Clean(rules.ExpandPath(p))
	if !filepath.IsAbs(p) {
		writeErr(w, http.StatusBadRequest, "path must be absolute")
		return
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "can't open this folder: "+err.Error())
		return
	}
	cfg, _ := config.Load()
	out := []folderEntry{}
	for _, e := range entries {
		if !e.IsDir() { // symlinked folders are skipped, like the scanner does
			continue
		}
		full := filepath.Join(p, e.Name())
		fe := folderEntry{Name: e.Name(), Path: full, Hidden: strings.HasPrefix(e.Name(), ".")}
		for _, m := range projectMarkers {
			if _, err := os.Lstat(filepath.Join(full, m)); err == nil {
				fe.Project = true
				break
			}
		}
		for _, root := range cfg.ScanRoots {
			fe.Root = fe.Root || filepath.Clean(root) == full
		}
		fe.Covered = !fe.Root && scanner.IsUnder(full, cfg.ScanRoots)
		fe.Excluded = scanner.IsUnder(full, cfg.Exclude)
		out = append(out, fe)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })

	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	shortcuts := []map[string]string{{"name": "Home", "path": home}}
	for _, n := range []string{"Desktop", "Documents", "Developer", "Projects", "projects", "code", "Code", "src", "repos", "workspace", "go/src", "AndroidStudioProjects", "IdeaProjects"} {
		sp := filepath.Join(home, n)
		if fi, err := os.Stat(sp); err == nil && fi.IsDir() {
			shortcuts = append(shortcuts, map[string]string{"name": n, "path": sp})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": p, "parent": parent, "home": home, "entries": out, "shortcuts": shortcuts,
		"native_picker": runtime.GOOS == "darwin",
		"scanned":       scanner.IsUnder(p, cfg.ScanRoots),
		"excluded":      scanner.IsUnder(p, cfg.Exclude),
	})
}

// chooseFolder opens the macOS folder dialog and returns the chosen path.
func (s *Server) chooseFolder(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "darwin" {
		writeErr(w, http.StatusNotImplemented, "the system folder dialog is only available on macOS")
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
	}
	if r.ContentLength > 0 && !decode(w, r, &req) {
		return
	}
	prompt := strings.NewReplacer(`\`, "", `"`, "").Replace(req.Prompt)
	if prompt == "" {
		prompt = "Choose a folder"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript",
		"-e", `tell application "System Events"`,
		"-e", "activate",
		"-e", `set f to choose folder with prompt "`+prompt+`"`,
		"-e", "end tell",
		"-e", "POSIX path of f").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && strings.Contains(string(exit.Stderr), "-128") {
			writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
			return
		}
		writeErr(w, http.StatusInternalServerError, "folder dialog failed: "+strings.TrimSpace(string(out)))
		return
	}
	p := filepath.Clean(strings.TrimSpace(string(out)))
	writeJSON(w, http.StatusOK, map[string]any{"path": p})
}

// folderList adds or removes one folder from scan_roots or exclude. It works
// on single paths (no comma splitting) and keeps scan locations from overlapping.
func (s *Server) folderList(w http.ResponseWriter, r *http.Request) {
	var req struct {
		List   string `json:"list"`   // "scan_roots" or "exclude"
		Action string `json:"action"` // "add" or "remove"
		Path   string `json:"path"`
	}
	if !decode(w, r, &req) {
		return
	}
	p := filepath.Clean(rules.ExpandPath(strings.TrimSpace(req.Path)))
	if req.Path == "" || !filepath.IsAbs(p) {
		writeErr(w, http.StatusBadRequest, "choose a full folder path")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var list *[]string
	switch req.List {
	case "scan_roots":
		list = &cfg.ScanRoots
	case "exclude":
		list = &cfg.Exclude
	default:
		writeErr(w, http.StatusBadRequest, "unknown list")
		return
	}
	note := ""
	switch req.Action {
	case "remove":
		kept := []string{}
		for _, x := range *list {
			if filepath.Clean(x) != p {
				kept = append(kept, x)
			}
		}
		*list = kept
	case "add":
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			writeErr(w, http.StatusBadRequest, "that folder doesn't exist")
			return
		}
		for _, x := range *list {
			if filepath.Clean(x) == p {
				writeJSON(w, http.StatusOK, map[string]any{"config": cfg, "note": "already added"})
				return
			}
		}
		if req.List == "scan_roots" {
			if scanner.IsUnder(p, cfg.ScanRoots) {
				writeErr(w, http.StatusBadRequest, "already covered by another scan location")
				return
			}
			// A parent replaces the scan locations inside it.
			kept := []string{}
			replaced := 0
			for _, x := range cfg.ScanRoots {
				if scanner.IsUnder(filepath.Clean(x), []string{p}) {
					replaced++
					continue
				}
				kept = append(kept, x)
			}
			if replaced > 0 {
				note = "replaced folders inside it"
			}
			cfg.ScanRoots = kept
		}
		*list = append(*list, p)
	default:
		writeErr(w, http.StatusBadRequest, "action must be add or remove")
		return
	}
	if err := config.Save(cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": cfg, "note": note})
}
