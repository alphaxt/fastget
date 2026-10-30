package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestStalledConnectionIsRetried: the server stops sending data part-way through
// the first response it serves. The watchdog must drop that connection and the
// segment must resume from where it stopped, producing the exact file.
func TestStalledConnectionIsRetried(t *testing.T) {
	old := stallTimeout
	stallTimeout = 400 * time.Millisecond
	defer func() { stallTimeout = old }()

	body := deterministicBody(512 * 1024)
	var stalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"stall"`)
		rng := r.Header.Get("Range")
		if rng == "" || rng == "bytes=0-0" || r.Method == http.MethodHead {
			http.ServeContent(w, r, "f.bin", testModTime, newReadSeeker(body))
			return
		}
		var from, to int
		fmt.Sscanf(strings.TrimPrefix(rng, "bytes="), "%d-%d", &from, &to)
		chunk := body[from : to+1]
		w.Header().Set("Content-Range", "bytes "+strconv.Itoa(from)+"-"+strconv.Itoa(to)+"/"+strconv.Itoa(len(body)))
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.WriteHeader(http.StatusPartialContent)
		if atomic.CompareAndSwapInt32(&stalled, 0, 1) {
			// First segment request: send half, then hang until the client leaves.
			w.Write(chunk[:len(chunk)/2])
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.Write(chunk)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "stall.bin")
	d := New(Options{URL: srv.URL, OutputPath: out, Connections: 4})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := d.Download(ctx, nil); err != nil {
		t.Fatalf("download with a stalled connection failed: %v", err)
	}
	got, _ := os.ReadFile(out)
	if sha(got) != sha(body) {
		t.Fatalf("file corrupted after stall recovery")
	}
}

// TestForbiddenIsNotRetried: a 403 must fail fast with a clear message instead of
// burning through retries.
func TestForbiddenIsNotRetried(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	d := New(Options{URL: srv.URL, OutputPath: filepath.Join(t.TempDir(), "x.bin"), Connections: 4})
	start := time.Now()
	err := d.Download(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected a 403 error, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("403 took %v; it should fail fast", time.Since(start))
	}
}
