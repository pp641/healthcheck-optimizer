//go:build !windows

package fsutil

import (
	"io/fs"
	"syscall"
)

func statOf(info fs.FileInfo) fileStat {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fileStat{
			alloc: int64(st.Blocks) * 512,
			nlink: uint64(st.Nlink),
			dev:   uint64(st.Dev),
			ino:   uint64(st.Ino),
		}
	}
	return fileStat{alloc: info.Size(), nlink: 1}
}
