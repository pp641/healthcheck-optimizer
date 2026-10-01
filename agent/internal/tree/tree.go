// Package tree measures every folder under the scan locations so the UI can
// answer "where did my space go?". It is read-only. Sizes are allocated disk
// blocks, with hard-linked files counted once. Heavy folders (dependency and
// VCS trees) are sized but not broken down, and excluded folders are shown
// locked without being read.
package tree

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tidyfleet/tidyfleet/agent/internal/fsutil"
)

// Heavy folders are summed as one node instead of being expanded.
var Heavy = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true, ".venv": true, "venv": true,
	"__pycache__": true, ".next": true, ".gradle": true, "Pods": true, "DerivedData": true, "target": true,
}

type Node struct {
	Name      string
	Path      string
	Size      int64
	Files     int64
	Collapsed bool // heavy folder: sized, not expanded
	Locked    bool // excluded in settings: never read
	Denied    bool // could not be read

	fileBytes int64 // files directly inside
	fileCount int64
	children  []*Node
	parent    *Node
}

type Tree struct {
	mu        sync.RWMutex
	roots     []*Node
	byPath    map[string]*Node
	scannedAt time.Time
	opt       Options
}

type Options struct {
	Exclude []string
	// Visited counts folders read so far, for progress reporting.
	Visited *atomic.Int64
}

// Build walks each root in parallel.
func Build(ctx context.Context, roots []string, opt Options) *Tree {
	t := &Tree{byPath: map[string]*Node{}, opt: opt, scannedAt: time.Now()}
	b := newBuilder(ctx, opt)
	var wg sync.WaitGroup
	for _, r := range roots {
		r = filepath.Clean(r)
		n := &Node{Name: r, Path: r}
		t.roots = append(t.roots, n)
		if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
			n.Denied = true
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); b.dir(n) }()
	}
	wg.Wait()
	for _, r := range t.roots {
		t.index(r)
	}
	return t
}

type builder struct {
	ctx   context.Context
	opt   Options
	sem   chan struct{}
	mu    sync.Mutex
	links map[[2]uint64]bool
}

func newBuilder(ctx context.Context, opt Options) *builder {
	return &builder{ctx: ctx, opt: opt, sem: make(chan struct{}, runtime.NumCPU()*2), links: map[[2]uint64]bool{}}
}

func (b *builder) excluded(p string) bool {
	for _, d := range b.opt.Exclude {
		d = filepath.Clean(d)
		if p == d || strings.HasPrefix(p, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// alloc returns a file's size, counting each hard-linked inode once.
func (b *builder) alloc(info fs.FileInfo) int64 {
	a, shared, key := fsutil.Allocated(info)
	if !shared {
		return a
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.links[key] {
		return 0
	}
	b.links[key] = true
	return a
}

func (b *builder) dir(n *Node) {
	if b.ctx.Err() != nil {
		return
	}
	entries, err := os.ReadDir(n.Path)
	if b.opt.Visited != nil {
		b.opt.Visited.Add(1)
	}
	if err != nil {
		n.Denied = true
		return
	}
	var wg sync.WaitGroup
	for _, e := range entries {
		p := filepath.Join(n.Path, e.Name())
		if e.IsDir() { // symlinks to folders are not followed (Type is ModeSymlink)
			c := &Node{Name: e.Name(), Path: p, parent: n}
			n.children = append(n.children, c)
			switch {
			case b.excluded(p):
				c.Locked = true
			case Heavy[e.Name()]:
				c.Collapsed = true
				b.sum(c)
			default:
				select { // recurse in parallel while workers are free, inline otherwise
				case b.sem <- struct{}{}:
					wg.Add(1)
					go func() { defer wg.Done(); defer func() { <-b.sem }(); b.dir(c) }()
				default:
					b.dir(c)
				}
			}
			continue
		}
		if info, err := e.Info(); err == nil {
			n.fileBytes += b.alloc(info)
			n.fileCount++
		}
	}
	wg.Wait()
	n.Size, n.Files = n.fileBytes, n.fileCount
	for _, c := range n.children {
		n.Size += c.Size
		n.Files += c.Files
	}
}

// sum sizes a collapsed folder without keeping its children.
func (b *builder) sum(n *Node) {
	_ = filepath.WalkDir(n.Path, func(p string, d fs.DirEntry, err error) error {
		if b.ctx.Err() != nil {
			return fs.SkipAll
		}
		if err != nil {
			if d != nil && d.IsDir() && p != n.Path {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if b.opt.Visited != nil {
				b.opt.Visited.Add(1)
			}
			return nil
		}
		if info, err := d.Info(); err == nil {
			n.Size += b.alloc(info)
			n.Files++
		}
		return nil
	})
}

func (t *Tree) index(n *Node) {
	t.byPath[n.Path] = n
	for _, c := range n.children {
		t.index(c)
	}
}

func (t *Tree) unindex(n *Node) {
	delete(t.byPath, n.Path)
	for _, c := range n.children {
		t.unindex(c)
	}
}

func (t *Tree) ScannedAt() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.scannedAt
}

// Contains reports whether path is a folder in the tree.
func (t *Tree) Contains(path string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.byPath[filepath.Clean(path)] != nil
}

// Entry is one row of a listing.
type Entry struct {
	Name        string `json:"name"`
	Path        string `json:"path,omitempty"`
	Kind        string `json:"kind"` // "dir" or "files" (the loose files directly inside a folder)
	Size        int64  `json:"size"`
	Files       int64  `json:"files"`
	HasChildren bool   `json:"has_children"`
	Collapsed   bool   `json:"collapsed,omitempty"`
	Locked      bool   `json:"locked,omitempty"`
	Denied      bool   `json:"denied,omitempty"`
}

func entry(n *Node) Entry {
	return Entry{
		Name: n.Name, Path: n.Path, Kind: "dir", Size: n.Size, Files: n.Files,
		HasChildren: len(n.children) > 0 || n.fileCount > 0, Collapsed: n.Collapsed, Locked: n.Locked, Denied: n.Denied,
	}
}

// Roots lists the scan locations.
func (t *Tree) Roots() []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Entry, 0, len(t.roots))
	for _, r := range t.roots {
		out = append(out, entry(r))
	}
	return out
}

// Children lists one level below path, largest first. ok is false when path
// is not a folder in the tree.
func (t *Tree) Children(path string) (self Entry, kids []Entry, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := t.byPath[filepath.Clean(path)]
	if n == nil {
		return Entry{}, nil, false
	}
	kids = make([]Entry, 0, len(n.children)+1)
	for _, c := range n.children {
		kids = append(kids, entry(c))
	}
	if n.fileCount > 0 {
		kids = append(kids, Entry{Name: "files", Kind: "files", Size: n.fileBytes, Files: n.fileCount})
	}
	sort.SliceStable(kids, func(i, j int) bool { return kids[i].Size > kids[j].Size })
	return entry(n), kids, true
}

func (t *Tree) adjust(n *Node, dSize, dFiles int64) {
	for p := n; p != nil; p = p.parent {
		p.Size += dSize
		p.Files += dFiles
	}
}

// Rescan re-measures one folder and updates its ancestors.
func (t *Tree) Rescan(ctx context.Context, path string) bool {
	t.mu.RLock()
	old := t.byPath[filepath.Clean(path)]
	t.mu.RUnlock()
	if old == nil || old.Locked {
		return false
	}
	fresh := &Node{Name: old.Name, Path: old.Path, Collapsed: old.Collapsed}
	b := newBuilder(ctx, t.opt)
	if fresh.Collapsed {
		b.sum(fresh)
	} else {
		b.dir(fresh)
	}
	if ctx.Err() != nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byPath[old.Path] != old {
		return false // changed meanwhile
	}
	t.unindex(old)
	dSize, dFiles := fresh.Size-old.Size, fresh.Files-old.Files
	old.children, old.fileBytes, old.fileCount, old.Denied = fresh.children, fresh.fileBytes, fresh.fileCount, fresh.Denied
	for _, c := range old.children {
		c.parent = old
	}
	t.adjust(old, dSize, dFiles)
	t.index(old)
	return true
}

// Remove drops a folder that was cleaned, subtracting its size upward.
func (t *Tree) Remove(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.byPath[filepath.Clean(path)]
	if n == nil || n.parent == nil {
		return
	}
	t.adjust(n.parent, -n.Size, -n.Files)
	kids := n.parent.children[:0]
	for _, c := range n.parent.children {
		if c != n {
			kids = append(kids, c)
		}
	}
	n.parent.children = kids
	t.unindex(n)
}

// Lock marks a folder excluded: its size no longer counts and it is not shown expanded.
func (t *Tree) Lock(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.byPath[filepath.Clean(path)]
	if n == nil || n.Locked {
		return
	}
	if n.parent != nil {
		t.adjust(n.parent, -n.Size, -n.Files)
	}
	for _, c := range n.children {
		t.unindex(c)
	}
	n.children, n.fileBytes, n.fileCount, n.Size, n.Files = nil, 0, 0, 0, 0
	n.Locked, n.Collapsed = true, false
	t.opt.Exclude = append(t.opt.Exclude, n.Path)
}

// SizeOf returns the measured size of a folder in the tree.
func (t *Tree) SizeOf(path string) (int64, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := t.byPath[filepath.Clean(path)]
	if n == nil {
		return 0, false
	}
	return n.Size, true
}
