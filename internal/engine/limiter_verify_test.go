package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRateLimiterThrottles downloads a known size with a rate cap and checks that
// the elapsed time is at least what the cap implies. Example: 1 MiB at 512 KiB/s
// must take roughly >= ~1.5s (the bucket starts with ~0.5s of burst).
func TestRateLimiterThrottles(t *testing.T) {
	size := 1 << 20 // 1 MiB
	body := deterministicBody(size)
	srv := rangeServer(t, body)
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "limited.bin")
	rate := int64(512 * 1024) // 512 KiB/s
	d := New(Options{URL: srv.URL, OutputPath: out, Connections: 4, MaxBytesPerSec: rate})

	start := time.Now()
	if err := d.Download(context.Background(), nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	elapsed := time.Since(start)

	// Lower bound: total bytes beyond the initial burst must be rate-limited.
	// burst ~= 0.5s * rate, so throttled bytes ~= size - burst.
	burst := float64(rate) * 0.5
	throttledBytes := float64(size) - burst
	minExpected := time.Duration(throttledBytes / float64(rate) * float64(time.Second))
	// Allow some slack below the theoretical minimum for scheduling jitter.
	floor := time.Duration(float64(minExpected) * 0.6)
	if elapsed < floor {
		t.Fatalf("download finished too fast for the rate cap: elapsed=%v, floor=%v", elapsed, floor)
	}

	// Sanity: content is still correct.
	got, _ := os.ReadFile(out)
	if sha(got) != sha(body) {
		t.Fatalf("limited download corrupted the file")
	}
}

// TestVerifySizeAndHashPass confirms a correct size + sha256 verify succeeds.
func TestVerifySizeAndHashPass(t *testing.T) {
	body := deterministicBody(256 * 1024)
	srv := rangeServer(t, body)
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "verified.bin")
	d := New(Options{
		URL:            srv.URL,
		OutputPath:     out,
		Connections:    4,
		ExpectedSize:   int64(len(body)),
		ExpectedSHA256: sha(body),
	})
	if err := d.Download(context.Background(), nil); err != nil {
		t.Fatalf("expected verify to pass, got: %v", err)
	}
	// Sidecar cleared on verified success.
	if _, err := os.Stat(statePath(out)); !os.IsNotExist(err) {
		t.Fatalf("sidecar should be cleared after verified success")
	}
}

// TestVerifyHashMismatchFails confirms a wrong hash makes Download fail and keeps
// the resume sidecar.
func TestVerifyHashMismatchFails(t *testing.T) {
	body := deterministicBody(256 * 1024)
	srv := rangeServer(t, body)
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "badhash.bin")
	d := New(Options{
		URL:            srv.URL,
		OutputPath:     out,
		Connections:    4,
		ExpectedSHA256: "deadbeef" + strings.Repeat("00", 28), // 64 hex chars, wrong
	})
	err := d.Download(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected verify failure, got nil")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch error, got: %v", err)
	}
	// Sidecar kept for retry.
	if _, serr := os.Stat(statePath(out)); serr != nil {
		t.Fatalf("sidecar should be kept after verify failure")
	}
}

// TestVerifySizeMismatchFails confirms a wrong expected size fails fast.
func TestVerifySizeMismatchFails(t *testing.T) {
	body := deterministicBody(100 * 1024)
	srv := rangeServer(t, body)
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "badsize.bin")
	d := New(Options{
		URL:          srv.URL,
		OutputPath:   out,
		Connections:  2,
		ExpectedSize: int64(len(body)) + 1, // off by one
	})
	err := d.Download(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("expected size mismatch error, got: %v", err)
	}
}
