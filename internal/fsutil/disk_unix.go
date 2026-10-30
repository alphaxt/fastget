//go:build linux || darwin

package fsutil

import (
	"errors"
	"syscall"
)

func freeSpace(dir string) (uint64, bool) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(dir, &s); err != nil {
		return 0, false
	}
	return uint64(s.Bavail) * uint64(s.Bsize), true
}

func isDiskFull(err error) bool {
	return errors.Is(err, syscall.ENOSPC)
}
