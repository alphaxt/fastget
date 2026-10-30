// Package fsutil holds the file-system helpers shared by the native host, the
// desktop app and the engine: safe file names, non-clobbering paths, the
// download folder, free-space checks and disk-full detection.
package fsutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SafeName picks a file name from the browser's suggestion, or from the URL if
// there is none, and replaces characters Windows does not allow in file names.
func SafeName(suggested, rawURL string) string {
	name := filepath.Base(strings.ReplaceAll(suggested, "\\", "/"))
	if name == "" || name == "." || name == "/" {
		u := strings.SplitN(strings.SplitN(rawURL, "?", 2)[0], "#", 2)[0]
		name = filepath.Base(strings.ReplaceAll(u, "\\", "/"))
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" || name == "_" {
		name = "download.bin"
	}
	return name
}

// UniquePath returns p, or "name (1).ext", "name (2).ext", ... so an existing
// file (or an unfinished download of the same name) is never overwritten.
func UniquePath(p string) string {
	if !exists(p) && !exists(p+".fgdl") {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 1; i < 10000; i++ {
		c := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if !exists(c) && !exists(c+".fgdl") {
			return c
		}
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// DownloadsDir returns the user's Downloads directory (home as a fallback).
func DownloadsDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		d := filepath.Join(home, "Downloads")
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
		return home
	}
	return "."
}

// EnsureDir checks that dir is an absolute folder fastget can write to,
// creating it if needed, and returns its cleaned form.
func EnsureDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", errors.New("no folder given")
	}
	if len(dir) == 2 && dir[1] == ':' {
		dir += `\` // "H:" means the root of drive H, not its current directory
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("%q is not a full folder path", dir)
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot use folder %s: %w", dir, err)
	}
	probe, err := os.CreateTemp(dir, ".fastget-write-test-*")
	if err != nil {
		return "", fmt.Errorf("cannot write to folder %s: %w", dir, err)
	}
	probe.Close()
	os.Remove(probe.Name())
	return dir, nil
}

// FreeSpace reports the bytes available to this user on the drive holding
// path (path itself may not exist yet). ok is false if it cannot be measured.
func FreeSpace(path string) (free uint64, ok bool) {
	d := path
	for {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			return 0, false
		}
		d = parent
	}
	return freeSpace(d)
}

// DriveOf names the drive or folder holding path, for messages ("H:\").
func DriveOf(path string) string {
	if v := filepath.VolumeName(path); v != "" {
		return v + `\`
	}
	return filepath.Dir(path)
}

// IsDiskFull reports whether err means the disk ran out of space.
func IsDiskFull(err error) bool {
	return err != nil && isDiskFull(err)
}

// HumanBytes formats a byte count for people (1.5 GB).
func HumanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n) / 1024
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
