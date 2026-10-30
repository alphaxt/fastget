package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCancelThenResume mirrors what Pause/Resume does in the queue: cancel a
// running download part-way, then start a new Downloader on the same output.
// The second run must continue from the saved progress and produce the exact
// original bytes.
func TestCancelThenResume(t *testing.T) {
	body := deterministicBody(4 << 20) // 4 MiB
	srv := rangeServer(t, body)
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "pr.bin")

	// Run 1: throttled so we can cancel it part-way.
	ctx, cancel := context.WithCancel(context.Background())
	d1 := New(Options{URL: srv.URL, OutputPath: out, Connections: 8, MaxBytesPerSec: 1 << 20})
	done := make(chan error, 1)
	go func() { done <- d1.Download(ctx, nil) }()
	time.Sleep(1500 * time.Millisecond)
	cancel()
	err1 := <-done
	if err1 == nil {
		t.Fatalf("first run finished before cancel; make the body bigger")
	}
	if _, err := os.Stat(statePath(out)); err != nil {
		t.Fatalf("resume state not saved after cancel: %v", err)
	}

	// Run 2: resume without a limit.
	d2 := New(Options{URL: srv.URL, OutputPath: out, Connections: 8})
	if err := d2.Download(context.Background(), nil); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if sha(got) != sha(body) {
		t.Fatalf("resumed file differs from the original")
	}
}
