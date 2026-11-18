//go:build windows || linux || (darwin && cgo)

// Package tray shows the system tray icon (fyne.io/systray).
package tray

import (
	"sync"

	"fyne.io/systray"
)

const Supported = true

// systray.Quit runs once and is lost if the tray window does not exist yet,
// so a Stop that comes before onReady is deferred until then.
var (
	mu      sync.Mutex
	ready   bool
	stopped bool
)

type Options struct {
	Tooltip string
	Machine string
	OnOpen  func()
	OnQuit  func() // called once when the user picks Quit or Stop is called
}

// Run shows the icon and blocks until Quit/Stop. Call it on the main goroutine.
func Run(o Options) {
	systray.Run(func() {
		mu.Lock()
		ready = true
		s := stopped
		mu.Unlock()
		if s {
			systray.Quit()
			return
		}
		systray.SetIcon(iconBytes())
		systray.SetTooltip(o.Tooltip)
		systray.SetOnTapped(o.OnOpen) // left click opens the UI; right click shows the menu
		open := systray.AddMenuItem("Open netchat", "Open the chat in your browser")
		info := systray.AddMenuItem("Machine: "+o.Machine, "")
		info.Disable()
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit", "Stop netchat on this machine")
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					o.OnOpen()
				case <-quit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, o.OnQuit)
}

// Stop removes the icon and makes Run return.
func Stop() {
	mu.Lock()
	stopped = true
	r := ready
	mu.Unlock()
	if r {
		systray.Quit()
	}
}
