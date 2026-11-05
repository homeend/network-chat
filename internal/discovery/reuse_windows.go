//go:build windows

package discovery

import "syscall"

// reuseControl sets SO_REUSEADDR so several instances on one host can share
// the beacon port (Windows has no SO_REUSEPORT).
func reuseControl(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	})
	if err != nil {
		return err
	}
	return serr
}
