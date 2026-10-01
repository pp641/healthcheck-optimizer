package fsutil

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// MoveToTrash follows the freedesktop.org trash spec for the home trash.
func MoveToTrash(path string) (string, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	files := filepath.Join(dataHome, "Trash", "files")
	info := filepath.Join(dataHome, "Trash", "info")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(info, 0o700); err != nil {
		return "", err
	}
	dest := uniqueName(files, filepath.Base(path))
	meta := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		(&url.URL{Path: path}).EscapedPath(), time.Now().Format("2006-01-02T15:04:05"))
	infoPath := filepath.Join(info, filepath.Base(dest)+".trashinfo")
	if err := os.WriteFile(infoPath, []byte(meta), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(path, dest); err != nil {
		os.Remove(infoPath)
		return "", err
	}
	return dest, nil
}
