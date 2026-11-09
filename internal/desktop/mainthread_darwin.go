//go:build darwin && !server

package desktop

import "runtime"

// AppKit must be driven from the process main thread, and glaze expects
// glaze.New to be called there. Go runs package init functions on the main
// thread but lets the main goroutine move to any thread afterwards unless it
// is locked during init. Without this, by the time Run creates the window the
// main goroutine can be on another thread, and [NSApp run] traps (SIGILL,
// seen reliably on amd64 under Rosetta).
func init() {
	runtime.LockOSThread()
}
