package scanner

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxActivityEntries = 20000

// activityTracker finds the last time a project was worked on: the newest
// modification among its own files (not build output) and git's HEAD/index.
type activityTracker struct {
	mu    sync.Mutex
	cache map[string]*activityEntry
	skip  map[string]bool
}

type activityEntry struct {
	once sync.Once
	t    time.Time
}

func newActivityTracker(skipNames []string) *activityTracker {
	skip := map[string]bool{".git": true, "node_modules": true}
	for _, n := range skipNames {
		skip[n] = true
	}
	return &activityTracker{cache: map[string]*activityEntry{}, skip: skip}
}

func (a *activityTracker) lastActivity(projectDir, repoRoot string) time.Time {
	a.mu.Lock()
	e := a.cache[projectDir]
	if e == nil {
		e = &activityEntry{}
		a.cache[projectDir] = e
	}
	a.mu.Unlock()
	e.once.Do(func() { e.t = a.compute(projectDir, repoRoot) })
	return e.t
}

func (a *activityTracker) compute(projectDir, repoRoot string) time.Time {
	var newest time.Time
	bump := func(t time.Time) {
		if t.After(newest) {
			newest = t
		}
	}
	if repoRoot != "" {
		for _, f := range []string{"HEAD", "index", filepath.Join("logs", "HEAD")} {
			if fi, err := os.Stat(filepath.Join(repoRoot, ".git", f)); err == nil {
				bump(fi.ModTime())
			}
		}
	}
	seen := 0
	base := strings.Count(projectDir, string(filepath.Separator))
	_ = filepath.WalkDir(projectDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != projectDir {
				return fs.SkipDir
			}
			return nil
		}
		if seen++; seen > maxActivityEntries {
			return fs.SkipAll
		}
		if d.IsDir() && p != projectDir {
			if a.skip[d.Name()] || strings.Count(p, string(filepath.Separator))-base > 4 {
				return fs.SkipDir
			}
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			bump(fi.ModTime())
		}
		return nil
	})
	return newest
}
