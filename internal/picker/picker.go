// Package picker shows the operating system's "choose a folder" dialog.
//
// Browser extensions and web pages cannot open a native folder picker, so the
// native host (for the confirm popup) and the desktop app (for its pages) call
// this instead.
package picker

import "errors"

// ErrCancelled is returned when the user closes the dialog without choosing.
var ErrCancelled = errors.New("no folder chosen")

// ErrUnsupported is returned on platforms without a picker implementation.
var ErrUnsupported = errors.New("folder picker is not available on this system")

// PickFolder shows the folder dialog, starting at initial if it exists, and
// returns the chosen folder.
func PickFolder(initial string) (string, error) {
	return pickFolder(initial)
}
