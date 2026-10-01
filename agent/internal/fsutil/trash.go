package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// uniqueName returns a path in dir for base that does not exist yet.
func uniqueName(dir, base string) string {
	p := filepath.Join(dir, base)
	if _, err := os.Lstat(p); os.IsNotExist(err) {
		return p
	}
	return filepath.Join(dir, fmt.Sprintf("%s %s", base, time.Now().Format("15.04.05.000")))
}
