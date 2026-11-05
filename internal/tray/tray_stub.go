//go:build !(windows || linux || (darwin && cgo))

package tray

const Supported = false

type Options struct {
	Tooltip string
	Machine string
	OnOpen  func()
	OnQuit  func()
}

var stop = make(chan struct{})

// Run blocks until Stop (no tray on this platform/build).
func Run(o Options) {
	<-stop
	if o.OnQuit != nil {
		o.OnQuit()
	}
}

func Stop() {
	select {
	case <-stop:
	default:
		close(stop)
	}
}
