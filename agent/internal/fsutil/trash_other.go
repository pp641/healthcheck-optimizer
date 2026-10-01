//go:build !darwin && !linux && !windows

package fsutil

import "errors"

func MoveToTrash(path string) (string, error) {
	return "", errors.New("trash is not supported on this platform; use permanent delete")
}
