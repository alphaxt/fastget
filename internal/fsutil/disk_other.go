//go:build !windows && !linux && !darwin

package fsutil

func freeSpace(dir string) (uint64, bool) { return 0, false }

func isDiskFull(err error) bool { return false }
