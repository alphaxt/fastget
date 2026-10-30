//go:build !windows

package picker

func pickFolder(initial string) (string, error) { return "", ErrUnsupported }
