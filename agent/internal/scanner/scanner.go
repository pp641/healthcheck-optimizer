// Package scanner finds regenerable developer artifacts and decides which of
// them are safe to clean. It never modifies anything.
package scanner

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/config"
	"github.com/tidyfleet/tidyfleet/agent/internal/fsutil"
	"github.com/tidyfleet/tidyfleet/agent/internal/rules"
)

type Item struct {
	ID          string    `json:"id"`
	RuleID      string    `json:"rule_id"`
	RuleName    string    `json:"rule_name"`
	Ecosystem   string    `json:"ecosystem"`
	Kind        string    `json:"kind"`
	Path        string    `json:"path,omitempty"`
	ProjectDir  string    `json:"project_dir,omitempty"`
	Detail      string    `json:"detail,omitempty"`
	SizeBytes   int64     `json:"size_bytes"`
	LastActive  time.Time `json:"last_active,omitempty"`
	StaleDays   int       `json:"stale_days,omitempty"`
	GitRepo     string    `json:"git_repo,omitempty"`
	GitDirty    bool      `json:"git_dirty,omitempty"`
	Eligible    bool      `json:"eligible"`
	SkipReason  string    `json:"skip_reason,omitempty"`
	CleanMethod string    `json:"clean_method"`
}

type Report struct {
	ScannedAt        time.Time `json:"scanned_at"`
	DurationMS       int64     `json:"duration_ms"`
	Roots            []string  `json:"roots"`
	Items            []Item    `json:"items"`
	ReclaimableBytes int64     `json:"reclaimable_bytes"`
	SkippedBytes     int64     `json:"skipped_bytes"`
	DirsVisited      int64     `json:"dirs_visited"`
	Warnings         []string  `json:"warnings,omitempty"`
}

// neverDescend are folders the walker never enters: VCS data, and dependency
// trees too large to be worth searching for nested projects.
var neverDescend = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true, ".Trash": true,
	".venv": true, "venv": true, "Pods": true, ".gradle": true, ".cache": true,
}

type scan struct {
	ctx     context.Context
	cfg     config.Config
	project map[string][]rules.Rule // dir name -> project rules
	git     *gitInfo
	act     *activityTracker

	mu       sync.Mutex
	items    []Item // project items, filled by the walk
	fixed    []Item // path and probe items, filled concurrently with assessment
	warnings []string
	dirs     int64
	sem      chan struct{}
	wg       sync.WaitGroup
}

// Scan walks cfg.ScanRoots and the rules' fixed locations. It is read-only.
func Scan(ctx context.Context, cfg config.Config, all []rules.Rule) Report {
	start := time.Now()
	s := &scan{
		ctx:     ctx,
		cfg:     cfg,
		project: map[string][]rules.Rule{},
		git:     newGitInfo(),
		sem:     make(chan struct{}, runtime.NumCPU()*4),
	}
	var artifactNames []string
	var fixed []rules.Rule
	for _, r := range all {
		if !r.AppliesToOS() || !cfg.RuleEnabled(r.ID) {
			continue
		}
		if r.Kind == rules.KindProject {
			for _, n := range r.Match.DirNames {
				s.project[n] = append(s.project[n], r)
				artifactNames = append(artifactNames, n)
			}
		} else if r.Available() {
			fixed = append(fixed, r)
		}
	}
	s.act = newActivityTracker(artifactNames)
	if !s.git.available {
		s.warn("git not found: project folders inside repos will be skipped")
	}

	for _, root := range cfg.ScanRoots {
		root = filepath.Clean(root)
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			s.warn(fmt.Sprintf("scan root %s is not accessible", root))
			continue
		}
		if s.excluded(root) {
			continue
		}
		s.wg.Add(1)
		go s.walk(root, 0)
	}
	s.wg.Wait()

	// Size and assess project items in parallel.
	var wg sync.WaitGroup
	pool := make(chan struct{}, runtime.NumCPU())
	for i := range s.items {
		wg.Add(1)
		pool <- struct{}{}
		go func(it *Item) {
			defer wg.Done()
			defer func() { <-pool }()
			s.assessProject(it)
		}(&s.items[i])
	}
	for _, r := range fixed {
		wg.Add(1)
		pool <- struct{}{}
		go func(r rules.Rule) {
			defer wg.Done()
			defer func() { <-pool }()
			s.scanFixed(r)
		}(r)
	}
	wg.Wait()

	rep := Report{
		ScannedAt:   start,
		DurationMS:  time.Since(start).Milliseconds(),
		Roots:       cfg.ScanRoots,
		Warnings:    s.warnings,
		DirsVisited: s.dirs,
	}
	for _, it := range append(s.items, s.fixed...) {
		if it.SizeBytes == 0 && it.Kind != rules.KindProject {
			continue
		}
		rep.Items = append(rep.Items, it)
		if it.Eligible {
			rep.ReclaimableBytes += it.SizeBytes
		} else {
			rep.SkippedBytes += it.SizeBytes
		}
	}
	sort.Slice(rep.Items, func(i, j int) bool { return rep.Items[i].SizeBytes > rep.Items[j].SizeBytes })
	return rep
}

func (s *scan) warn(msg string) {
	s.mu.Lock()
	s.warnings = append(s.warnings, msg)
	s.mu.Unlock()
}

func (s *scan) excluded(path string) bool {
	return IsUnder(path, s.cfg.Exclude)
}

// IsUnder reports whether path equals or lies inside any of dirs.
func IsUnder(path string, dirs []string) bool {
	for _, d := range dirs {
		d = filepath.Clean(d)
		if path == d || strings.HasPrefix(path, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (s *scan) walk(dir string, depth int) {
	defer s.wg.Done()
	if s.ctx.Err() != nil {
		return
	}
	s.sem <- struct{}{}
	entries, err := os.ReadDir(dir)
	<-s.sem
	if err != nil {
		return
	}
	s.mu.Lock()
	s.dirs++
	s.mu.Unlock()

	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	for _, e := range entries {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		child := filepath.Join(dir, e.Name())
		if s.excluded(child) {
			continue
		}
		if r, ok := s.matchProject(e.Name(), names); ok {
			s.mu.Lock()
			s.items = append(s.items, Item{
				ID:          itemID(r.ID, child),
				RuleID:      r.ID,
				RuleName:    r.Name,
				Ecosystem:   r.Ecosystem,
				Kind:        r.Kind,
				Path:        child,
				ProjectDir:  dir,
				CleanMethod: rules.MethodTrash,
			})
			s.mu.Unlock()
			continue // never descend into matched build output
		}
		if neverDescend[e.Name()] || depth+1 > s.cfg.MaxDepth {
			continue
		}
		s.wg.Add(1)
		go s.walk(child, depth+1)
	}
}

func (s *scan) matchProject(name string, siblings map[string]bool) (rules.Rule, bool) {
	for _, r := range s.project[name] {
		for _, marker := range r.Match.SiblingAny {
			if siblings[marker] {
				return r, true
			}
		}
	}
	return rules.Rule{}, false
}

func (s *scan) ruleByID(id string) rules.Rule {
	for _, list := range s.project {
		for _, r := range list {
			if r.ID == id {
				return r
			}
		}
	}
	return rules.Rule{}
}

func (s *scan) assessProject(it *Item) {
	r := s.ruleByID(it.RuleID)
	it.SizeBytes = fsutil.DiskUsage(it.Path).Freeable
	it.GitRepo = s.git.repoRoot(it.ProjectDir)
	it.LastActive = s.act.lastActivity(it.ProjectDir, it.GitRepo)
	if !it.LastActive.IsZero() {
		it.StaleDays = int(time.Since(it.LastActive).Hours() / 24)
	}
	it.Eligible, it.SkipReason = s.eligibility(r, it)
}

func (s *scan) eligibility(r rules.Rule, it *Item) (bool, string) {
	if it.GitRepo != "" {
		if it.Path == it.GitRepo || it.Path == it.ProjectDir {
			return false, "is a repository or project root"
		}
		tracked, err := s.git.isTracked(s.ctx, it.GitRepo, it.Path)
		if err != nil {
			return false, "could not read git state"
		}
		if tracked {
			return false, "contains files tracked by git"
		}
		if r.RequireGitignored {
			ignored, err := s.git.isIgnored(s.ctx, it.GitRepo, it.Path)
			if err != nil {
				return false, "could not read git state"
			}
			if !ignored {
				return false, "not gitignored"
			}
		}
		dirty, err := s.git.isDirty(s.ctx, it.GitRepo)
		if err != nil {
			return false, "could not read git status"
		}
		it.GitDirty = dirty
		if dirty && !s.cfg.CleanDirtyRepos {
			return false, "repo has uncommitted changes"
		}
	} else if r.RequireGitignored {
		return false, "not in a git repo, so cannot confirm it is build output"
	}
	if r.StaleCheck && s.cfg.StaleDays > 0 && it.StaleDays < s.cfg.StaleDays {
		return false, fmt.Sprintf("project active %d days ago (threshold %d)", it.StaleDays, s.cfg.StaleDays)
	}
	return true, ""
}

func (s *scan) scanFixed(r rules.Rule) {
	method := r.Clean.Method
	if method == rules.MethodCommand && r.Clean.Fallback != "" {
		if _, err := exec.LookPath(r.Clean.Commands[0][0]); err != nil {
			method = r.Clean.Fallback
		}
	}
	switch r.Kind {
	case rules.KindPath:
		for _, p := range r.ResolvePaths() {
			if fi, err := os.Lstat(p); err != nil || !fi.IsDir() {
				continue
			}
			it := Item{
				ID: itemID(r.ID, p), RuleID: r.ID, RuleName: r.Name, Ecosystem: r.Ecosystem,
				Kind: r.Kind, Path: p, CleanMethod: method, Eligible: true,
			}
			if s.excluded(p) {
				it.Eligible, it.SkipReason = false, "in an excluded folder"
			}
			it.SizeBytes = fsutil.DiskUsage(p).Freeable
			s.mu.Lock()
			s.fixed = append(s.fixed, it)
			s.mu.Unlock()
		}
	case rules.KindProbe:
		probe := probes[r.Match.Probe]
		if probe == nil {
			s.warn(fmt.Sprintf("rule %s: unknown probe %q", r.ID, r.Match.Probe))
			return
		}
		res, err := probe(s.ctx)
		if err != nil {
			s.warn(fmt.Sprintf("%s: %v", r.Name, err))
			return
		}
		s.mu.Lock()
		s.fixed = append(s.fixed, Item{
			ID: itemID(r.ID, ""), RuleID: r.ID, RuleName: r.Name, Ecosystem: r.Ecosystem,
			Kind: r.Kind, Detail: res.detail, SizeBytes: res.bytes, CleanMethod: method, Eligible: true,
		})
		s.mu.Unlock()
	}
}

func itemID(ruleID, path string) string {
	h := sha1.Sum([]byte(ruleID + "\x00" + path))
	return hex.EncodeToString(h[:])[:8]
}
