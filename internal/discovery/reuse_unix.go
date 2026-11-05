//go:build linux || darwin || freebsd || netbsd || openbsd

package discovery

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reuseControl sets SO_REUSEADDR and SO_REUSEPORT so several instances on one
// host can share the beacon port.
func reuseControl(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		if serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); serr != nil {
			return
		}
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	})
	if err != nil {
		return err
	}
	return serr
}
