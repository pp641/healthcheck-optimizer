package fsutil

import (
	"fmt"
	"os/exec"
	"strings"
)

// MoveToTrash sends a folder to the Recycle Bin through .NET's FileSystem API.
func MoveToTrash(path string) (string, error) {
	esc := strings.ReplaceAll(path, "'", "''")
	script := "Add-Type -AssemblyName Microsoft.VisualBasic; " +
		"[Microsoft.VisualBasic.FileIO.FileSystem]::DeleteDirectory('" + esc + "', " +
		"'OnlyErrorDialogs', 'SendToRecycleBin')"
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("recycle bin: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return "Recycle Bin", nil
}
