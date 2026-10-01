package scanner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// gitInfo caches repository lookups and status for one scan.
type gitInfo struct {
	mu        sync.Mutex
	roots     map[string]string // dir -> repo root ("" when not in a repo)
	dirty     map[string]*repoStatus
	available bool
}

type repoStatus struct {
	once  sync.Once
	dirty bool
	err   error
}

func newGitInfo() *gitInfo {
	_, err := exec.LookPath("git")
	return &gitInfo{roots: map[string]string{}, dirty: map[string]*repoStatus{}, available: err == nil}
}

// repoRoot returns the enclosing git work tree of dir, or "".
func (g *gitInfo) repoRoot(dir string) string {
	g.mu.Lock()
	if r, ok := g.roots[dir]; ok {
		g.mu.Unlock()
		return r
	}
	g.mu.Unlock()
	root := ""
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			root = d
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	g.mu.Lock()
	g.roots[dir] = root
	g.mu.Unlock()
	return root
}

// isDirty reports uncommitted changes, including untracked non-ignored files.
func (g *gitInfo) isDirty(ctx context.Context, root string) (bool, error) {
	g.mu.Lock()
	st := g.dirty[root]
	if st == nil {
		st = &repoStatus{}
		g.dirty[root] = st
	}
	g.mu.Unlock()
	st.once.Do(func() {
		out, err := g.run(ctx, root, "status", "--porcelain")
		st.dirty, st.err = len(bytes.TrimSpace(out)) > 0, err
	})
	return st.dirty, st.err
}

// isTracked reports whether git tracks any file under path.
func (g *gitInfo) isTracked(ctx context.Context, root, path string) (bool, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false, err
	}
	out, err := g.run(ctx, root, "ls-files", "-z", "--", rel)
	return len(out) > 0, err
}

// isIgnored reports whether git ignores the folder at path.
func (g *gitInfo) isIgnored(ctx context.Context, root, path string) (bool, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false, err
	}
	_, err = g.run(ctx, root, "check-ignore", "-q", "--", filepath.ToSlash(rel)+"/")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return err == nil, err
}

func (g *gitInfo) run(ctx context.Context, root string, args ...string) ([]byte, error) {
	if !g.available {
		return nil, errors.New("git is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0") // never take the index lock
	return cmd.Output()
}
