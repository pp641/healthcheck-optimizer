package tree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func mk(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func find(kids []Entry, name string) *Entry {
	for i := range kids {
		if kids[i].Name == name {
			return &kids[i]
		}
	}
	return nil
}

func TestBuildSizesAndShapes(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "big", "a.bin"), 400_000)
	mk(t, filepath.Join(root, "big", "deep", "b.bin"), 200_000)
	mk(t, filepath.Join(root, "small", "c.txt"), 10)
	mk(t, filepath.Join(root, "app", "node_modules", "x", "index.js"), 100_000)
	mk(t, filepath.Join(root, "secret", "private.txt"), 300_000)
	mk(t, filepath.Join(root, "loose.bin"), 50_000)
	// A hard link must be counted once.
	if err := os.Link(filepath.Join(root, "big", "a.bin"), filepath.Join(root, "small", "a-link.bin")); err != nil {
		t.Fatal(err)
	}
	// A symlinked folder must not be followed.
	os.Symlink(filepath.Join(root, "big"), filepath.Join(root, "big-link"))

	tr := Build(context.Background(), []string{root}, Options{Exclude: []string{filepath.Join(root, "secret")}})
	self, kids, ok := tr.Children(root)
	if !ok {
		t.Fatal("root missing")
	}
	sum := int64(0)
	for _, k := range kids {
		sum += k.Size
	}
	if sum != self.Size {
		t.Errorf("children sum %d != folder size %d", sum, self.Size)
	}
	for i := 1; i < len(kids); i++ {
		if kids[i].Size > kids[i-1].Size {
			t.Errorf("not sorted largest first: %v", kids)
		}
	}
	// The hard link is counted once, in whichever folder was read first.
	big, small := find(kids, "big"), find(kids, "small")
	if both := big.Size + small.Size; both < 600_000 || both > 700_000 {
		t.Errorf("hard link counted twice? big+small = %d", both)
	}
	if s := find(kids, "secret"); s == nil || !s.Locked || s.Size != 0 {
		t.Errorf("excluded folder should be locked with no size: %+v", s)
	}
	if f := find(kids, "files"); f == nil || f.Kind != "files" || f.Files != 2 { // loose.bin + big-link symlink
		t.Errorf("loose files entry: %+v", f)
	}
	_, appKids, _ := tr.Children(filepath.Join(root, "app"))
	nm := find(appKids, "node_modules")
	if nm == nil || !nm.Collapsed || nm.Size < 100_000 {
		t.Fatalf("node_modules should be collapsed and sized: %+v", nm)
	}
	if _, k, _ := tr.Children(filepath.Join(root, "app", "node_modules")); nm.HasChildren || len(k) != 0 {
		t.Error("collapsed folder should not be expandable")
	}

	// Remove subtracts upward.
	before := tr.Roots()[0].Size
	tr.Remove(filepath.Join(root, "app", "node_modules"))
	if after := tr.Roots()[0].Size; before-after != nm.Size {
		t.Errorf("remove: root went %d -> %d, want -%d", before, after, nm.Size)
	}

	// Rescan picks up new files.
	mk(t, filepath.Join(root, "small", "new.bin"), 500_000)
	before = tr.Roots()[0].Size
	if !tr.Rescan(context.Background(), filepath.Join(root, "small")) {
		t.Fatal("rescan failed")
	}
	if after := tr.Roots()[0].Size; after-before < 500_000 {
		t.Errorf("rescan: root grew %d, want >= 500000", after-before)
	}

	// Lock hides a folder's size.
	tr.Lock(filepath.Join(root, "big"))
	_, kids, _ = tr.Children(root)
	if b := find(kids, "big"); !b.Locked || b.Size != 0 {
		t.Errorf("locked: %+v", b)
	}
	if tr.Contains(filepath.Join(root, "big", "deep")) {
		t.Error("locked folder's children still indexed")
	}
}
