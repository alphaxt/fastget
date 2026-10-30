package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// deterministicBody builds a reproducible byte slice so range stitching can be
// verified against a known whole-file hash.
func deterministicBody(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*31 + 7) % 251)
	}
	return b
}

// rangeServer serves a fixed body with full HTTP Range support, so the engine's
// multi-segment and resume paths can be exercised without the network.
func rangeServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"test-etag-1"`)
		http.ServeContent(w, r, "file.bin", testModTime, newReadSeeker(body))
	}))
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// TestMultiSegmentMatchesWhole verifies that an N-connection download produces a
// file byte-identical to the original body.
func TestMultiSegmentMatchesWhole(t *testing.T) {
	body := deterministicBody(1 << 20) // 1 MiB
	srv := rangeServer(t, body)
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "out.bin")
	d := New(Options{URL: srv.URL, OutputPath: out, Connections: 8})
	if err := d.Download(context.Background(), nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if sha(got) != sha(body) {
		t.Fatalf("hash mismatch: got %s want %s (len got=%d want=%d)", sha(got), sha(body), len(got), len(body))
	}
	// Sidecar must be cleared on success.
	if _, err := os.Stat(statePath(out)); !os.IsNotExist(err) {
		t.Fatalf("sidecar should be removed after success")
	}
}

// TestResumeFromPartialState writes a partial resume sidecar (as if a previous run
// was interrupted), then resumes and verifies the final file is correct and that
// only the missing bytes were fetched.
func TestResumeFromPartialState(t *testing.T) {
	body := deterministicBody(1 << 20) // 1 MiB
	total := int64(len(body))

	var served int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"test-etag-1"`)
		// Count bytes actually served to prove resume didn't refetch everything.
		cw := &countingWriter{ResponseWriter: w, n: &served}
		http.ServeContent(cw, r, "file.bin", testModTime, newReadSeeker(body))
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "out.bin")

	// Pre-create the output at full size and pre-fill the first half, then write a
	// sidecar that marks 2 of 4 segments complete.
	if err := os.WriteFile(out, make([]byte, total), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(out, os.O_RDWR, 0o644)
	half := total / 2
	if _, err := f.WriteAt(body[:half], 0); err != nil {
		t.Fatal(err)
	}
	f.Close()

	quarter := total / 4
	st := &State{
		URL:        srv.URL,
		OutputPath: out,
		TotalSize:  total,
		ETag:       `"test-etag-1"`,
		Resumable:  true,
		Segments: []Segment{
			{Index: 0, Start: 0, End: quarter - 1, Downloaded: quarter},      // complete
			{Index: 1, Start: quarter, End: half - 1, Downloaded: quarter},   // complete
			{Index: 2, Start: half, End: half + quarter - 1, Downloaded: 0},  // to fetch
			{Index: 3, Start: half + quarter, End: total - 1, Downloaded: 0}, // to fetch
		},
	}
	if err := saveState(st); err != nil {
		t.Fatal(err)
	}

	// Resume. Connections here are ignored because a resumable state already exists.
	d := New(Options{URL: srv.URL, OutputPath: out, Connections: 4})
	if err := d.Download(context.Background(), nil); err != nil {
		t.Fatalf("resume download: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if sha(got) != sha(body) {
		t.Fatalf("resumed file hash mismatch")
	}
	// Only the second half (plus the 1-byte probe) should have been served.
	if served > half+1024 {
		t.Fatalf("resume refetched too much: served=%d, expected ~%d", served, half)
	}
	if _, err := os.Stat(statePath(out)); !os.IsNotExist(err) {
		t.Fatalf("sidecar should be removed after success")
	}
	fmt.Printf("resume served %d bytes (half=%d) - only missing bytes fetched\n", served, half)
}
