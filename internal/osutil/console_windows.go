//go:build windows

package osutil

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	user32                  = syscall.NewLazyDLL("user32.dll")
	procGetConsoleProcesses = kernel32.NewProc("GetConsoleProcessList")
	procGetConsoleWindow    = kernel32.NewProc("GetConsoleWindow")
	procFreeConsole         = kernel32.NewProc("FreeConsole")
	procShowWindow          = user32.NewProc("ShowWindow")
	procMessageBox          = user32.NewProc("MessageBoxW")
)

// HideConsoleIfOwned hides the console window when this process is its only
// user, i.e. netchat.exe was started by double-click rather than from a shell.
// It reports whether the console was hidden.
func HideConsoleIfOwned() bool {
	var pids [4]uint32
	n, _, _ := procGetConsoleProcesses.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 {
		return false // started from a terminal: keep logging there
	}
	if hwnd, _, _ := procGetConsoleWindow.Call(); hwnd != 0 {
		procShowWindow.Call(hwnd, 0) // SW_HIDE
	}
	procFreeConsole.Call()
	return true
}

// Alert shows a modal error dialog.
func Alert(title, msg string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(msg)
	procMessageBox.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10) // MB_ICONERROR
}
