package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestSharedLimiterCapsCombinedSpeed: two downloads sharing one limiter must
// together stay near the limit (the "Speed limit" setting is a total).
func TestSharedLimiterCapsCombinedSpeed(t *testing.T) {
	body := deterministicBody(1 << 20)
	srv := rangeServer(t, body)
	defer srv.Close()

	lim := NewRateLimiter(1 << 20) // 1 MiB/s for both together
	dir := t.TempDir()
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out := filepath.Join(dir, string(rune('a'+i))+".bin")
			if err := New(Options{URL: srv.URL, OutputPath: out, Connections: 4, Limiter: lim}).Download(context.Background(), nil); err != nil {
				t.Errorf("download %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	// 2 MiB at 1 MiB/s: about 2 s (minus the 0.5 s burst). Two separate
	// per-download limits would finish in about 1 s.
	if el := time.Since(start); el < 1300*time.Millisecond {
		t.Fatalf("combined speed not capped: 2 MiB took only %v at a 1 MiB/s total limit", el)
	}
}

// TestLimitChangesApplyImmediately: raising the limit speeds up a running
// download without restarting it.
func TestLimitChangesApplyImmediately(t *testing.T) {
	body := deterministicBody(4 << 20)
	srv := rangeServer(t, body)
	defer srv.Close()
	lim := NewRateLimiter(256 << 10) // very slow: 4 MiB would take 16 s
	go func() {
		time.Sleep(500 * time.Millisecond)
		lim.SetRate(0) // remove the limit
	}()
	start := time.Now()
	out := filepath.Join(t.TempDir(), "x.bin")
	if err := New(Options{URL: srv.URL, OutputPath: out, Connections: 4, Limiter: lim}).Download(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("removing the limit did not take effect: took %v", el)
	}
}

// TestHTTP1Only: every segment must get its own TCP connection, which HTTP/2
// would prevent (it multiplexes all requests over one connection).
func TestHTTP1Only(t *testing.T) {
	tr, ok := New(Options{URL: "http://x"}).client.Transport.(*http.Transport)
	if !ok || tr.TLSNextProto == nil || len(tr.TLSNextProto) != 0 || tr.ForceAttemptHTTP2 {
		t.Fatalf("HTTP/2 is not disabled on the download transport")
	}
}

// TestResumeFileStaysSmall: finished pieces are merged when saved.
func TestResumeFileStaysSmall(t *testing.T) {
	st := &State{URL: "u", OutputPath: filepath.Join(t.TempDir(), "f.bin"), TotalSize: 4096 << 20, Resumable: true}
	st.Segments = splitRange(0, st.TotalSize-1, 1<<20) // 4096 pieces
	for i := range st.Segments[:3000] {
		s := &st.Segments[i]
		s.Downloaded = s.End - s.Start + 1
	}
	if err := saveState(st); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(statePath(st.OutputPath))
	loaded, _ := loadState(st.OutputPath)
	if loaded.bytesDone() != st.bytesDone() {
		t.Fatalf("progress lost on save: %d vs %d", loaded.bytesDone(), st.bytesDone())
	}
	if len(loaded.Segments) > 1100 {
		t.Fatalf("finished pieces were not merged: %d entries", len(loaded.Segments))
	}
	t.Logf("resume file: %d bytes, %d entries", fi.Size(), len(loaded.Segments))
}

// TestConnLimitSharedBetweenDownloads: two downloads sharing a limit of 4
// connections never have more than 4 requests in flight together, and both
// finish correctly.
func TestConnLimitSharedBetweenDownloads(t *testing.T) {
	body := deterministicBody(2 << 20)
	var inFlight, maxSeen int32
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > maxSeen {
			maxSeen = inFlight
		}
		mu.Unlock()
		defer func() { mu.Lock(); inFlight--; mu.Unlock() }()
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(w, r, "f.bin", testModTime, newReadSeeker(body))
	}))
	defer srv.Close()

	cl := NewConnLimit(4)
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out := filepath.Join(dir, string(rune('a'+i))+".bin")
			if err := New(Options{URL: srv.URL, OutputPath: out, Connections: 8, ConnLimit: cl}).Download(context.Background(), nil); err != nil {
				t.Errorf("download %d: %v", i, err)
				return
			}
			got, _ := os.ReadFile(out)
			if sha(got) != sha(body) {
				t.Errorf("download %d corrupted", i)
			}
		}(i)
	}
	wg.Wait()
	// +2 for the two 1-byte probe requests, which do not take a slot.
	if maxSeen > 4+2 {
		t.Fatalf("%d requests in flight at once; the shared limit is 4", maxSeen)
	}
}

// TestConnLimitFairShare: with three downloads joined, none gets more than its
// share and none is starved.
func TestConnLimitFairShare(t *testing.T) {
	c := NewConnLimit(8)
	a, b, d3 := &Downloader{}, &Downloader{}, &Downloader{}
	c.join(a)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if err := c.acquire(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	c.join(b)
	c.join(d3)
	// a holds 8; b must get a slot as soon as a gives some back.
	got := make(chan *Downloader, 16)
	for _, dl := range []*Downloader{b, b, b, d3, d3, d3} {
		go func(dl *Downloader) {
			if c.acquire(ctx, dl) == nil {
				got <- dl
			}
		}(dl)
	}
	// a releases 6 (finishes pieces); with share 3 it may not take them back.
	for i := 0; i < 6; i++ {
		c.release(a)
	}
	deadline := time.After(2 * time.Second)
	counts := map[*Downloader]int{}
	for i := 0; i < 5; i++ { // a keeps 2, total 8 => b and d3 get 6 slots minus... at least 5
		select {
		case dl := <-got:
			counts[dl]++
		case <-deadline:
			t.Fatalf("starved: b=%d d3=%d", counts[b], counts[d3])
		}
	}
	if counts[b] == 0 || counts[d3] == 0 {
		t.Fatalf("unfair split: b=%d d3=%d", counts[b], counts[d3])
	}
	// a may refill up to its share (3) but never beyond it.
	for c.acquireTimeout(a) == nil {
	}
	c.mu.Lock()
	held, share, used := c.per[a], c.shareLocked(), c.used
	c.mu.Unlock()
	if held > share {
		t.Fatalf("a holds %d connections, more than its share %d", held, share)
	}
	if used > 8 {
		t.Fatalf("%d connections in use, limit is 8", used)
	}
}

// TestYieldHandsConnectionToWaitingDownload: a download holding all 8
// connections gives one up as soon as another download is waiting.
func TestYieldHandsConnectionToWaitingDownload(t *testing.T) {
	c := NewConnLimit(8)
	a, b := &Downloader{}, &Downloader{}
	c.join(a)
	for i := 0; i < 8; i++ {
		c.acquire(context.Background(), a)
	}
	if c.yield(a) {
		t.Fatalf("yielded with nobody waiting")
	}
	c.join(b)
	got := make(chan bool, 1)
	go func() { got <- c.acquire(context.Background(), b) == nil }()
	time.Sleep(50 * time.Millisecond)
	if !c.yield(a) {
		t.Fatalf("a did not yield although b is waiting")
	}
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatalf("b did not get the yielded connection")
	}
}

func (c *ConnLimit) acquireTimeout(d *Downloader) error {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	return c.acquire(ctx, d)
}

// throttledServer serves body with Range support, limiting every connection to
// perConn bytes/s, like typical download servers.
func throttledServer(t *testing.T, body []byte, perConn int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"thr"`)
		http.ServeContent(&slowWriter{w: w, rate: perConn}, r, "f.bin", testModTime, newReadSeeker(body))
	}))
}

type slowWriter struct {
	w    http.ResponseWriter
	rate int
}

func (s *slowWriter) Header() http.Header { return s.w.Header() }
func (s *slowWriter) WriteHeader(c int)   { s.w.WriteHeader(c) }
func (s *slowWriter) Write(p []byte) (int, error) {
	const step = 16 << 10
	n := 0
	for len(p) > 0 {
		k := step
		if k > len(p) {
			k = len(p)
		}
		m, err := s.w.Write(p[:k])
		n += m
		if err != nil {
			return n, err
		}
		if f, ok := s.w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(time.Duration(float64(k) / float64(s.rate) * float64(time.Second)))
		p = p[k:]
	}
	return n, nil
}

// TestMultiDownloadThroughput: with a per-connection server limit, one
// download and three downloads from the same server must reach about the same
// total speed (8 shared connections), and the three must share it fairly.
func TestMultiDownloadThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	const perConn = 512 << 10 // 512 KB/s per connection
	body := deterministicBody(64 << 20)
	srv := throttledServer(t, body, perConn)
	defer srv.Close()

	measure := func(n int) (total float64, each []float64) {
		cl := NewConnLimit(8)
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		dir := t.TempDir()
		got := make([]int64, n)
		var wg sync.WaitGroup
		start := time.Now()
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				out := filepath.Join(dir, string(rune('a'+i))+".bin")
				_ = New(Options{URL: srv.URL, OutputPath: out, Connections: 8, ConnLimit: cl}).
					Download(ctx, func(p Progress) { got[i] = p.Downloaded })
			}(i)
		}
		wg.Wait()
		el := time.Since(start).Seconds()
		for _, g := range got {
			each = append(each, float64(g)/el/(1<<20))
			total += float64(g) / el / (1 << 20)
		}
		return
	}

	one, _ := measure(1)
	three, each := measure(3)
	t.Logf("1 download: %.2f MB/s; 3 downloads: %.2f MB/s total, each %.2f / %.2f / %.2f", one, three, each[0], each[1], each[2])
	ideal := 8 * float64(perConn) / (1 << 20) // 4 MB/s
	if one < ideal*0.6 {
		t.Fatalf("single download too slow: %.2f MB/s (ideal %.1f)", one, ideal)
	}
	if three < one*0.75 {
		t.Fatalf("three downloads lose total speed: %.2f vs %.2f MB/s", three, one)
	}
	for i, e := range each {
		if e < three/3*0.4 {
			t.Fatalf("download %d starved: %.2f MB/s of %.2f total", i, e, three)
		}
	}
}
