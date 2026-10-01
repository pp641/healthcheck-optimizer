// Package fsutil holds filesystem helpers: accurate disk usage and moving
// items to the OS trash.
package fsutil

import (
	"io/fs"
	"path/filepath"
)

// Usage describes how much space a folder takes.
type Usage struct {
	// Freeable is the disk space actually released if the folder is removed.
	// Hard-linked files only count when every link lives inside the folder
	// (e.g. pnpm's node_modules links into the global store and frees nothing).
	Freeable int64 `json:"freeable_bytes"`
	// Allocated counts every file's allocated blocks once (like `du`).
	Allocated int64 `json:"allocated_bytes"`
	Files     int64 `json:"files"`
}

type inodeKey struct{ dev, ino uint64 }

type linkState struct {
	seen, nlink uint64
	blocks      int64
}

// DiskUsage walks root without following symlinks. Unreadable entries are skipped.
// Sizes use allocated blocks, so sparse files are counted by what they occupy.
// APFS clones share blocks invisibly to stat and are counted in full.
func DiskUsage(root string) Usage {
	var u Usage
	links := map[inodeKey]*linkState{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		st := statOf(info)
		u.Files++
		if st.nlink <= 1 || d.IsDir() {
			u.Allocated += st.alloc
			u.Freeable += st.alloc
			return nil
		}
		k := inodeKey{st.dev, st.ino}
		ls := links[k]
		if ls == nil {
			ls = &linkState{nlink: st.nlink, blocks: st.alloc}
			links[k] = ls
			u.Allocated += st.alloc
		}
		ls.seen++
		return nil
	})
	for _, ls := range links {
		if ls.seen >= ls.nlink {
			u.Freeable += ls.blocks
		}
	}
	return u
}

type fileStat struct {
	alloc    int64
	nlink    uint64
	dev, ino uint64
}

// Allocated returns the disk space a file occupies, and whether it is one of
// several hard links (key identifies the shared inode).
func Allocated(info fs.FileInfo) (alloc int64, shared bool, key [2]uint64) {
	st := statOf(info)
	return st.alloc, st.nlink > 1 && !info.IsDir(), [2]uint64{st.dev, st.ino}
}
