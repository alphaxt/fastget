//go:build !windows

package tray

// run on non-Windows platforms has no tray; it simply blocks until the context
// is cancelled so the app can still run headless.
func run(opts Options) error {
	<-opts.Context.Done()
	if opts.OnQuit != nil {
		opts.OnQuit()
	}
	return nil
}
