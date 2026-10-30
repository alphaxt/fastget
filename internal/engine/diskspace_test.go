package engine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNotEnoughDiskSpace: a server that claims a file far larger than any disk
// must fail fast with the friendly "not enough free space" error, before any
// file is created.
func TestNotEnoughDiskSpace(t *testing.T) {
	const huge = int64(1) << 60 // 1 EiB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes 0-0/"+strconv.FormatInt(huge, 10))
		w.Header().Set("Content-Length", "1")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte{0})
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "huge.bin")
	var sawSize int64
	err := New(Options{URL: srv.URL, OutputPath: out, Connections: 4}).
		Download(context.Background(), func(p Progress) { sawSize = p.TotalSize })

	var de *diskSpaceError
	if !errors.As(err, &de) {
		t.Fatalf("expected a disk space error, got %v", err)
	}
	if !strings.Contains(err.Error(), "not enough free space") || !strings.Contains(err.Error(), "Change folder") {
		t.Fatalf("message is not helpful: %v", err)
	}
	if sawSize != huge {
		t.Fatalf("UI was not told the file size before failing (got %d)", sawSize)
	}
	if _, serr := os.Stat(out); !os.IsNotExist(serr) {
		t.Fatalf("an output file was created even though it cannot fit")
	}
}
