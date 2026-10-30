// Package tray provides a minimal system-tray presence for the desktop app.
//
// It is intentionally dependency-free: the Windows implementation calls the
// Win32 notification-area APIs through the standard library's syscall package
// (no cgo, no third-party tray library), and every other platform gets a no-op
// that simply blocks until the context is cancelled. This keeps the whole
// project building with CGO_ENABLED=0 on every OS.
package tray

import "context"

// Options configures the tray.
type Options struct {
	Title   string // tooltip text
	OnOpen  func() // invoked when the user picks "Open"
	OnQuit  func() // invoked when the user picks "Quit" (before Run returns)
	Context context.Context
}

// Run shows the tray icon (where supported) and blocks until the user quits or
// the context is cancelled. On unsupported platforms, or if the icon cannot be
// created, it falls back to simply waiting on the context so the app keeps
// running headless. The returned error is non-nil only for a hard failure the
// caller should surface.
func Run(opts Options) error {
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	return run(opts)
}
