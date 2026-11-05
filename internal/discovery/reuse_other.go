//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd

package discovery

import "syscall"

func reuseControl(_, _ string, _ syscall.RawConn) error { return nil }
