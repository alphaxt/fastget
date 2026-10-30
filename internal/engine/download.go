package engine

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yourname/fastget/internal/fsutil"
)

// Downloader runs a single multi-connection download.
type Downloader struct {
	opts    Options
	client  *http.Client
	limiter *RateLimiter // throughput cap; nil means unlimited

	downloaded int64 // atomic: total bytes written across all segments
}

// New builds a Downloader with sensible defaults applied.
func New(opts Options) *Downloader {
	opts.applyDefaults()
	limiter := opts.Limiter
	if limiter == nil {
		limiter = newRateLimiter(opts.MaxBytesPerSec)
	}
	return &Downloader{
		opts:    opts,
		limiter: limiter,
		client: &http.Client{
			// No client-wide timeout: a large segment may legitimately stream for
			// many minutes. Connect and response-header time are bounded by the
			// Transport below; stalled bodies are caught by a per-read idle check.
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   20 * time.Second, // fail fast when a server does not answer
					KeepAlive: 30 * time.Second,
				}).DialContext,
				// HTTP/1.1 only. Over HTTP/2 all "connections" to a server share
				// one TCP connection, which removes the whole point of
				// multi-connection downloading. A non-nil empty TLSNextProto map
				// disables HTTP/2.
				ForceAttemptHTTP2:     false,
				TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
				MaxConnsPerHost:       0,
				MaxIdleConns:          opts.Connections * 2,
				MaxIdleConnsPerHost:   opts.Connections * 2,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   opts.Timeout,
				ResponseHeaderTimeout: opts.Timeout, // bounds connect/first-byte, not the full body
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
	}
}

// applyHeaders sets the default and caller-supplied headers on a request.
func (d *Downloader) applyHeaders(req *http.Request) {
	req.Header.Set("User-Agent", d.opts.UserAgent)
	if d.opts.Referer != "" {
		req.Header.Set("Referer", d.opts.Referer)
	}
	if d.opts.Cookie != "" {
		req.Header.Set("Cookie", d.opts.Cookie)
	}
	// Accept anything; some servers 403 requests without an Accept header.
	req.Header.Set("Accept", "*/*")
	for k, v := range d.opts.Headers {
		req.Header.Set(k, v)
	}
}

// Download fetches the target described by Options, calling onProgress (if set)
// roughly every 500ms with a live snapshot. It blocks until the download is
// complete, the context is cancelled, or an unrecoverable error occurs.
func (d *Downloader) Download(ctx context.Context, onProgress func(Progress)) error {
	if d.opts.OutputPath == "" {
		name, err := fileNameFromURL(d.opts.URL)
		if err != nil {
			return err
		}
		d.opts.OutputPath = name
	}
	if dir := filepath.Dir(d.opts.OutputPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	// Connecting can fail transiently (slow or briefly unreachable server), so
	// retry it like the segments are retried. HTTP errors such as 403/404 are
	// permanent and reported straight away.
	var info targetInfo
	var err error
	for attempt := 0; ; attempt++ {
		info, err = d.probe(ctx)
		if err == nil {
			break
		}
		if ctx.Err() != nil || attempt >= d.opts.MaxRetries || isPermanent(err) {
			return fmt.Errorf("could not start download: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * d.opts.RetryWait):
		}
	}

	// Refuse early if the file cannot fit, before planning or creating anything.
	if err := d.checkSpace(info.size); err != nil {
		if onProgress != nil {
			onProgress(Progress{TotalSize: info.size, Connections: d.opts.Connections})
		}
		return err
	}

	st := d.planState(info)

	// Report the size straight away, so the UI shows it even if the next step
	// (reserving disk space) fails.
	if onProgress != nil {
		onProgress(Progress{TotalSize: st.TotalSize, Downloaded: st.bytesDone(), Connections: d.opts.Connections})
	}

	// Check free space and create the output file. Each piece is then written
	// at its own offset.
	if err := d.ensureOutputFile(st); err != nil {
		return err
	}

	atomic.StoreInt64(&d.downloaded, st.bytesDone())

	// Progress reporter.
	stopProgress := make(chan struct{})
	var progressWG sync.WaitGroup
	if onProgress != nil {
		progressWG.Add(1)
		go d.reportProgress(st, onProgress, stopProgress, &progressWG)
	}

	// Fan out one goroutine per incomplete segment.
	var wg sync.WaitGroup
	errCh := make(chan error, len(st.Segments))
	// Periodically persist progress so a crash is resumable.
	// Save resume data every 2 s, and only when something changed.
	saveTicker := time.NewTicker(2 * time.Second)
	defer saveTicker.Stop()
	go func() {
		saved := int64(-1)
		for {
			select {
			case <-stopProgress:
				return
			case <-saveTicker.C:
				if now := atomic.LoadInt64(&d.downloaded); now != saved {
					if saveState(st) == nil {
						saved = now
					}
				}
			}
		}
	}()

	if st.Resumable {
		// A fixed pool of connections takes the pieces in file order. When one
		// piece fails for good, the pool stops handing out new pieces.
		var failed int32
		workers := d.opts.Connections
		d.opts.ConnLimit.join(d)
		defer d.opts.ConnLimit.leave(d)

		// Pieces are handed out in file order. A piece interrupted because its
		// connection was given to another download goes back to the front.
		var qmu sync.Mutex
		next := 0
		var returned []int
		take := func() (int, bool) {
			qmu.Lock()
			defer qmu.Unlock()
			if n := len(returned); n > 0 {
				i := returned[n-1]
				returned = returned[:n-1]
				return i, true
			}
			for next < len(st.Segments) {
				i := next
				next++
				if !st.Segments[i].complete() {
					return i, true
				}
			}
			return 0, false
		}
		giveBack := func(i int) {
			qmu.Lock()
			returned = append(returned, i)
			qmu.Unlock()
		}

		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for atomic.LoadInt32(&failed) == 0 && ctx.Err() == nil {
					// Wait for a free connection to this server (shared fairly
					// with other downloads from it) before taking a piece.
					if err := d.opts.ConnLimit.acquire(ctx, d); err != nil {
						return
					}
					i, ok := take()
					if !ok {
						d.opts.ConnLimit.release(d)
						return
					}
					err := d.runSegment(ctx, st, &st.Segments[i])
					if err == errYield {
						giveBack(i) // the slot was already handed over
						continue
					}
					d.opts.ConnLimit.release(d)
					if err != nil {
						if atomic.CompareAndSwapInt32(&failed, 0, 1) {
							errCh <- err
						}
						return
					}
				}
			}()
		}
	}

	if !st.Resumable {
		// Server does not support ranges: fall back to a single stream.
		err := d.runSingleStream(ctx, st)
		close(stopProgress)
		progressWG.Wait()
		if err != nil {
			_ = saveState(st)
			return err
		}
		if verr := d.verify(); verr != nil {
			_ = saveState(st)
			return verr
		}
		clearState(d.opts.OutputPath)
		if onProgress != nil {
			onProgress(Progress{TotalSize: st.TotalSize, Downloaded: atomic.LoadInt64(&d.downloaded), Done: true})
		}
		return nil
	}

	wg.Wait()
	close(errCh)
	close(stopProgress)
	progressWG.Wait()

	// Collect the first error, if any.
	for e := range errCh {
		if e != nil {
			_ = saveState(st) // keep progress for a later resume
			return e
		}
	}

	// Paused or cancelled between two pieces: keep the progress, do not report
	// success.
	if ctx.Err() != nil {
		_ = saveState(st)
		return ctx.Err()
	}
	for i := range st.Segments {
		if !st.Segments[i].complete() {
			_ = saveState(st)
			return fmt.Errorf("download stopped before all parts were received")
		}
	}

	if err := d.finishFile(st); err != nil {
		_ = saveState(st)
		return err
	}

	// All segments finished: verify integrity before declaring success. A failed
	// verification keeps the sidecar so the user can retry/resume.
	if err := d.verify(); err != nil {
		_ = saveState(st)
		return err
	}

	// Success: remove the sidecar.
	clearState(d.opts.OutputPath)
	if onProgress != nil {
		onProgress(Progress{TotalSize: st.TotalSize, Downloaded: atomic.LoadInt64(&d.downloaded), Connections: d.opts.Connections, Done: true})
	}
	return nil
}

// targetInfo captures what the probe HEAD/GET learned about the remote file.
type targetInfo struct {
	size         int64
	acceptRanges bool
	etag         string
	lastModified string
}

// probe asks for the first byte of the file to learn its size, whether the
// server supports byte ranges (needed for multi-connection and resume), and the
// validators used to detect a changed file on resume.
func (d *Downloader) probe(ctx context.Context) (targetInfo, error) {
	var info targetInfo

	// One 1-byte ranged GET tells us everything: a 206 reply proves range
	// support and carries the total size (Content-Range), ETag and
	// Last-Modified. A separate HEAD request would only add latency; some
	// servers answer HEAD slowly or not at all.
	rangeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, d.opts.URL, nil)
	if err != nil {
		return info, err
	}
	d.applyHeaders(rangeReq)
	rangeReq.Header.Set("Range", "bytes=0-0")
	rResp, err := d.client.Do(rangeReq)
	if err != nil {
		if ctx.Err() == nil {
			return info, fmt.Errorf("server is not responding (%w)", err)
		}
		return info, err
	}
	defer rResp.Body.Close()
	// Read at most a little: a server that ignores Range sends the whole file.
	_, _ = io.CopyN(io.Discard, rResp.Body, 64*1024)
	if rResp.StatusCode >= 400 {
		return info, &statusError{code: rResp.StatusCode}
	}

	if rResp.StatusCode == http.StatusPartialContent {
		info.acceptRanges = true
		if info.size <= 0 {
			// Content-Range: bytes 0-0/12345 -> total is after the slash.
			if cr := rResp.Header.Get("Content-Range"); cr != "" {
				if idx := strings.LastIndex(cr, "/"); idx != -1 {
					if n, perr := strconv.ParseInt(strings.TrimSpace(cr[idx+1:]), 10, 64); perr == nil {
						info.size = n
					}
				}
			}
		}
		if info.etag == "" {
			info.etag = rResp.Header.Get("ETag")
		}
		if info.lastModified == "" {
			info.lastModified = rResp.Header.Get("Last-Modified")
		}
	} else if rResp.StatusCode >= 200 && rResp.StatusCode < 300 {
		// Server ignored the Range header and returned the whole body: no ranges.
		info.acceptRanges = false
		if info.size <= 0 {
			info.size = rResp.ContentLength
		}
	}

	return info, nil
}

// planState builds a fresh segment plan, or reuses a resumable saved one.
func (d *Downloader) planState(info targetInfo) *State {
	if saved, _ := loadState(d.opts.OutputPath); saved != nil {
		if saved.canResume(d.opts.URL, info.size, info.etag, info.lastModified) {
			// The download may have been moved to another folder while paused.
			saved.OutputPath = d.opts.OutputPath
			rechunk(saved, d.opts.Connections)
			return saved
		}
	}

	st := &State{
		URL:          d.opts.URL,
		OutputPath:   d.opts.OutputPath,
		TotalSize:    info.size,
		ETag:         info.etag,
		LastModified: info.lastModified,
		Resumable:    info.acceptRanges && info.size > 0,
		CreatedAt:    time.Now(),
	}

	if !st.Resumable {
		// One pseudo-segment spanning the whole (possibly unknown) file.
		st.Segments = []Segment{{Index: 0, Start: 0, End: info.size - 1}}
		return st
	}

	st.Segments = splitRange(0, st.TotalSize-1, chunkSize(st.TotalSize, d.opts.Connections))
	for i := range st.Segments {
		st.Segments[i].Index = i
	}
	return st
}

// chunkSize picks the piece size for a download. The file is cut into many
// small pieces that the connections take in order from the start of the file.
// Writes therefore stay close together, which matters on Windows: writing far
// beyond what has been written so far makes the OS fill the gap with zeros
// first (gigabytes on a large file, and very slow on USB disks). Small pieces
// also balance speed: a slow connection only ever holds one piece.
func chunkSize(total int64, conns int) int64 {
	if conns < 1 {
		conns = 1
	}
	c := total / int64(conns*4)
	// 4 MB pieces at most: connections hand pieces back often enough that the
	// per-server share can be rebalanced quickly when a download starts or ends.
	const minChunk, maxChunk, maxPieces = 256 << 10, 4 << 20, 4096
	if c < minChunk {
		c = minChunk
	}
	if c > maxChunk {
		c = maxChunk
	}
	// Keep the resume file small on very large downloads.
	if floor := (total + maxPieces - 1) / maxPieces; c < floor {
		c = floor
	}
	return c
}

// splitRange cuts the inclusive byte range [start, end] into pieces of at most
// size bytes.
func splitRange(start, end, size int64) []Segment {
	var segs []Segment
	for s := start; s <= end; s += size {
		e := s + size - 1
		if e > end {
			e = end
		}
		segs = append(segs, Segment{Start: s, End: e})
	}
	return segs
}

// rechunk turns a saved plan back into small pieces: finished parts stay as
// they are and every unfinished remainder is cut up again. Plans saved by
// older versions (one huge range per connection) resume this way too.
func rechunk(st *State, conns int) {
	size := chunkSize(st.TotalSize, conns)
	var out []Segment
	for _, s := range st.Segments {
		if s.complete() {
			out = append(out, s)
			continue
		}
		if s.Downloaded > 0 {
			out = append(out, Segment{Start: s.Start, End: s.Start + s.Downloaded - 1, Downloaded: s.Downloaded})
		}
		out = append(out, splitRange(s.Start+s.Downloaded, s.End, size)...)
	}
	for i := range out {
		out[i].Index = i
	}
	st.Segments = out
}

// checkSpace fails with a clear message if the rest of the file cannot fit on
// the destination drive.
func (d *Downloader) checkSpace(total int64) error {
	if total <= 0 {
		return nil
	}
	var have int64
	if fi, err := os.Stat(d.opts.OutputPath); err == nil {
		have = fi.Size() // a resumed download already holds this much
	}
	if need := total - have; need > 0 {
		if free, ok := fsutil.FreeSpace(d.opts.OutputPath); ok && uint64(need) > free {
			return &diskSpaceError{path: d.opts.OutputPath, need: uint64(need), free: free}
		}
	}
	return nil
}

// ensureOutputFile creates the destination file if it does not exist yet.
func (d *Downloader) ensureOutputFile(st *State) error {
	// The file is not pre-sized: it grows as pieces arrive in file order. Making
	// it full size up front would occupy the whole size on disk before anything
	// is downloaded, and on Windows a write far past the written data forces the
	// OS to zero-fill the gap first.
	f, err := os.OpenFile(d.opts.OutputPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return d.diskErr(err)
	}
	return f.Close()
}

// finishFile makes sure the finished file has exactly the expected length.
func (d *Downloader) finishFile(st *State) error {
	if !st.Resumable || st.TotalSize <= 0 {
		return nil
	}
	fi, err := os.Stat(d.opts.OutputPath)
	if err != nil {
		return err
	}
	if fi.Size() != st.TotalSize {
		return os.Truncate(d.opts.OutputPath, st.TotalSize)
	}
	return nil
}

// diskErr turns a "disk full" OS error into a clear message.
func (d *Downloader) diskErr(err error) error {
	if fsutil.IsDiskFull(err) {
		free, _ := fsutil.FreeSpace(d.opts.OutputPath)
		return &diskSpaceError{path: d.opts.OutputPath, free: free}
	}
	return err
}

// runSegment downloads one segment, retrying transient failures with linear
// backoff, and writing bytes to their absolute offset in the output file.
func (d *Downloader) runSegment(ctx context.Context, st *State, seg *Segment) error {
	var lastErr error
	failures := 0
	for {
		if seg.complete() {
			return nil
		}
		before := seg.Downloaded
		err := d.fetchSegment(ctx, st, seg)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err() // paused, cancelled or shut down
		}
		if err == errYield || isPermanent(err) {
			return err // 403, 404, ...: retrying will not help
		}
		lastErr = err
		if seg.Downloaded > before {
			failures = 0 // the connection was working; a drop is not a strike
		} else {
			failures++
		}
		if failures > d.opts.MaxRetries {
			return fmt.Errorf("gave up after %d failed attempts: %w", failures, lastErr)
		}
		wait := time.Duration(failures) * d.opts.RetryWait
		if wait > 10*time.Second {
			wait = 10 * time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// fetchSegment performs one ranged GET for the not-yet-downloaded tail of a segment.
func (d *Downloader) fetchSegment(ctx context.Context, st *State, seg *Segment) error {
	from := seg.Start + seg.Downloaded
	if from > seg.End {
		return nil
	}

	// Use the download-level context directly. Connect/first-byte time is bounded
	// by the Transport's ResponseHeaderTimeout, so a long but healthy body stream
	// is never aborted by a per-request deadline.
	// reqCtx lets the stall watchdog drop this one connection without touching
	// the rest of the download.
	reqCtx, cancelReq := context.WithCancel(ctx)
	defer cancelReq()
	watchdog := time.AfterFunc(stallTimeout, cancelReq)
	defer watchdog.Stop()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, d.opts.URL, nil)
	if err != nil {
		return err
	}
	d.applyHeaders(req)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, seg.End))

	resp, err := d.client.Do(req)
	if err != nil {
		return stalledOr(ctx, reqCtx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return &statusError{code: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("server ignored the byte range (HTTP %d)", resp.StatusCode)
	}

	f, err := os.OpenFile(d.opts.OutputPath, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return err
	}

	buf := make([]byte, 64*1024)
	nextYieldCheck := time.Now().Add(time.Second)
	for {
		// Once a second: if another download from this server is waiting and
		// this one has more than its share, hand this connection over.
		if now := time.Now(); now.After(nextYieldCheck) {
			nextYieldCheck = now.Add(time.Second)
			if d.opts.ConnLimit.yield(d) {
				return errYield
			}
		}
		watchdog.Reset(stallTimeout)
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			// Pause the watchdog while the speed limiter deliberately waits.
			watchdog.Stop()
			if err := d.limiter.wait(ctx, n); err != nil {
				return err
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				return d.diskErr(werr)
			}
			atomic.AddInt64(&seg.Downloaded, int64(n)) // read concurrently by saveState
			atomic.AddInt64(&d.downloaded, int64(n))
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return stalledOr(ctx, reqCtx, readErr)
		}
	}
}

// runSingleStream handles servers that do not support Range requests. There is no
// parallelism or resume here; it is a correct fallback, not the fast path.
func (d *Downloader) runSingleStream(ctx context.Context, st *State) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.opts.URL, nil)
	if err != nil {
		return err
	}
	d.applyHeaders(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{code: resp.StatusCode}
	}

	f, err := os.OpenFile(d.opts.OutputPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, 64*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if err := d.limiter.wait(ctx, n); err != nil {
				return err
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				return d.diskErr(werr)
			}
			atomic.AddInt64(&st.Segments[0].Downloaded, int64(n))
			atomic.AddInt64(&d.downloaded, int64(n))
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

// reportProgress emits a Progress snapshot about twice per second, computing an
// instantaneous speed over each sampling window.
func (d *Downloader) reportProgress(st *State, onProgress func(Progress), stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Speed is averaged over the last 5 seconds. A half-second snapshot jumps
	// between 0 and spikes because data arrives in bursts.
	type sample struct {
		t time.Time
		n int64
	}
	const window = 5 * time.Second
	hist := []sample{{time.Now(), atomic.LoadInt64(&d.downloaded)}}
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			cur := atomic.LoadInt64(&d.downloaded)
			hist = append(hist, sample{now, cur})
			for len(hist) > 2 && now.Sub(hist[1].t) >= window {
				hist = hist[1:]
			}
			var speed float64
			if dt := now.Sub(hist[0].t).Seconds(); dt > 0 {
				speed = float64(cur-hist[0].n) / dt
			}
			onProgress(Progress{
				TotalSize:   st.TotalSize,
				Downloaded:  cur,
				SpeedBps:    speed,
				Connections: d.opts.Connections,
			})
		}
	}
}

// fileNameFromURL derives a destination filename from the URL path.
func fileNameFromURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	name := filepath.Base(u.Path)
	if name == "" || name == "." || name == "/" {
		return "download.bin", nil
	}
	return name, nil
}
