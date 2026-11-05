// Package osutil has small OS integrations: open a URL, hide the console
// window when launched by double-click, and show an error dialog.
package osutil

import (
	"os/exec"
	"runtime"
)

// OpenURL opens url in the default browser.
func OpenURL(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
