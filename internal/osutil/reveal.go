package osutil

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

// Reveal shows path in the system file manager (selected where supported).
func Reveal(path string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", "/select,", path).Start()
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	}
}
