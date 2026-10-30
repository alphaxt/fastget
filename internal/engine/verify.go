package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// verify checks the finished output against the caller's expectations. It runs
// only when ExpectedSize or ExpectedSHA256 is set; otherwise it is a no-op.
//
// Size is checked first (cheap). The SHA-256 check streams the file so it works
// for large downloads without loading them into memory.
func (d *Downloader) verify() error {
	if d.opts.ExpectedSize <= 0 && d.opts.ExpectedSHA256 == "" {
		return nil
	}

	fi, err := os.Stat(d.opts.OutputPath)
	if err != nil {
		return fmt.Errorf("verify: cannot stat output: %w", err)
	}

	if d.opts.ExpectedSize > 0 && fi.Size() != d.opts.ExpectedSize {
		return fmt.Errorf("verify: size mismatch: got %d bytes, expected %d", fi.Size(), d.opts.ExpectedSize)
	}

	if d.opts.ExpectedSHA256 != "" {
		sum, err := fileSHA256(d.opts.OutputPath)
		if err != nil {
			return fmt.Errorf("verify: hashing failed: %w", err)
		}
		want := strings.ToLower(strings.TrimSpace(d.opts.ExpectedSHA256))
		if sum != want {
			return fmt.Errorf("verify: sha256 mismatch: got %s, expected %s", sum, want)
		}
	}
	return nil
}

// fileSHA256 streams a file through SHA-256 and returns the lowercase hex digest.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
