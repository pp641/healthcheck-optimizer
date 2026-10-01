//go:build windows

package fsutil

import "io/fs"

// Windows: fall back to apparent size; hard links are rare in dev folders there.
func statOf(info fs.FileInfo) fileStat {
	return fileStat{alloc: info.Size(), nlink: 1}
}
