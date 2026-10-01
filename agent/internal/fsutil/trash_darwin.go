package fsutil

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// MoveToTrash moves path into the user's Trash. Same-volume items are renamed
// into ~/.Trash; items on other volumes go through Finder.
func MoveToTrash(path string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dest := uniqueName(filepath.Join(home, ".Trash"), filepath.Base(path))
	err = os.Rename(path, dest)
	if err == nil {
		return dest, nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return "", err
	}
	esc := strings.ReplaceAll(strings.ReplaceAll(path, `\`, `\\`), `"`, `\"`)
	script := fmt.Sprintf(`tell application "Finder" to delete (POSIX file "%s")`, esc)
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return "", fmt.Errorf("finder trash: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return "Trash", nil
}
