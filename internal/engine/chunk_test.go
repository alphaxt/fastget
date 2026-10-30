package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestWritesStayNearTheFront is the regression test for "stuck at 0 B on a big
// file": the first requests must ask for the start of the file, not for
// offsets gigabytes away (which made Windows zero-fill the gap first and made
// the download look frozen).
func TestWritesStayNearTheFront(t *testing.T) {
	const total = int64(10) << 30 // a 10 GiB file
	var mu sync.Mutex
	var firstRanges []int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := strings.TrimPrefix(r.Header.Get("Range"), "bytes=")
		from, _ := strconv.ParseInt(strings.SplitN(rng, "-", 2)[0], 10, 64)
		if rng != "0-0" {
			mu.Lock()
			firstRanges = append(firstRanges, from)
			mu.Unlock()
		}
		w.Header().Set("Content-Range", "bytes 0-0/"+strconv.FormatInt(total, 10))
		// Refuse every piece so the test ends quickly; only the offsets matter.
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	// Use a fake huge free-space answer by downloading into the temp dir; the
	// 403 stops the run before anything large is written.
	out := filepath.Join(t.TempDir(), "big.bin")
	d := New(Options{URL: srv.URL, OutputPath: out, Connections: 8})
	info := targetInfo{size: total, acceptRanges: true}
	st := d.planState(info)

	if n := len(st.Segments); n < 8 || n > 4096 {
		t.Fatalf("unexpected number of pieces: %d", n)
	}
	// The first piece for each of the 8 connections must lie within the first
	// 8 * chunk bytes, i.e. all connections start near the beginning.
	chunk := st.Segments[0].End - st.Segments[0].Start + 1
	limit := chunk * 8
	for i := 0; i < 8; i++ {
		if st.Segments[i].Start >= limit {
			t.Fatalf("piece %d starts at %d, far from the front (limit %d)", i, st.Segments[i].Start, limit)
		}
	}
	if chunk > 4<<20 {
		t.Fatalf("pieces are too big (%d bytes) for a 10 GiB file", chunk)
	}

	_ = d.Download(context.Background(), nil)
	mu.Lock()
	defer mu.Unlock()
	for _, from := range firstRanges {
		if from >= limit {
			t.Fatalf("a connection asked for offset %d first; must stay below %d", from, limit)
		}
	}
	if fi, err := os.Stat(out); err == nil && fi.Size() > 0 {
		t.Fatalf("file should not be pre-sized, got %d bytes", fi.Size())
	}
}

// TestRechunkOldPlan: a plan saved by the old version (8 huge ranges, some
// progress) resumes as small pieces without losing the bytes already done.
func TestRechunkOldPlan(t *testing.T) {
	const total = int64(800) << 20
	part := total / 8
	st := &State{TotalSize: total, Resumable: true}
	var done int64
	for i := int64(0); i < 8; i++ {
		got := int64(0)
		if i%2 == 0 {
			got = 3 << 20
		}
		done += got
		st.Segments = append(st.Segments, Segment{Index: int(i), Start: i * part, End: (i+1)*part - 1, Downloaded: got})
	}
	rechunk(st, 8)
	if st.bytesDone() != done {
		t.Fatalf("bytes done changed: %d -> %d", done, st.bytesDone())
	}
	// Pieces must cover the whole file exactly once, in order.
	var pos int64
	for i, s := range st.Segments {
		if s.Index != i || s.Start != pos || s.End < s.Start {
			t.Fatalf("bad piece %d: %+v (expected start %d)", i, s, pos)
		}
		pos = s.End + 1
	}
	if pos != total {
		t.Fatalf("pieces cover %d bytes, want %d", pos, total)
	}
}
