//go:build !windows

package osutil

import (
	"fmt"
	"os"
)

func HideConsoleIfOwned() bool { return false }

func Alert(title, msg string) { fmt.Fprintf(os.Stderr, "%s: %s\n", title, msg) }
