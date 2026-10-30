// Package engine implements a clean-room multi-connection download engine.
//
// Design goals (the capabilities that make tools like IDM feel fast), all built
// from standard, public HTTP techniques:
//   - Multi-connection: split a file into N byte ranges and fetch them in parallel
//     using the HTTP Range header.
//   - Resume: persist each segment's progress so an interrupted download continues
//     from where it stopped instead of restarting.
//   - Retry with backoff: transient network errors on a single segment are retried
//     without failing the whole download.
//   - Live speed reporting: aggregate bytes/sec across all segments.
//
// None of this is derived from any proprietary binary; it is the standard way
// parallel HTTP downloading works.
package engine

import "time"

// Segment describes one contiguous byte range of the target file and how much of
// it has already been written. Offsets are absolute positions in the output file.
type Segment struct {
	Index      int   `json:"index"`
	Start      int64 `json:"start"`      // first byte (inclusive)
	End        int64 `json:"end"`        // last byte (inclusive)
	Downloaded int64 `json:"downloaded"` // bytes written so far for this segment
}

// remaining returns how many bytes of this segment still need downloading.
func (s *Segment) remaining() int64 {
	total := s.End - s.Start + 1
	return total - s.Downloaded
}

// complete reports whether the segment has been fully downloaded.
func (s *Segment) complete() bool {
	return s.remaining() <= 0
}

// State is the persisted, resumable description of a download. It is written to a
// sidecar ".fgdl" file next to the output so an interrupted run can resume.
type State struct {
	URL          string    `json:"url"`
	OutputPath   string    `json:"output_path"`
	TotalSize    int64     `json:"total_size"`    // -1 when the server does not report a length
	ETag         string    `json:"etag"`          // used to detect the remote file changing between runs
	LastModified string    `json:"last_modified"` // fallback validator when ETag is absent
	Segments     []Segment `json:"segments"`
	Resumable    bool      `json:"resumable"` // whether the server supports Range requests
	CreatedAt    time.Time `json:"created_at"`
}

// bytesDone sums downloaded bytes across all segments.
func (st *State) bytesDone() int64 {
	var n int64
	for i := range st.Segments {
		n += st.Segments[i].Downloaded
	}
	return n
}

// Options configures a Download run.
type Options struct {
	URL         string
	OutputPath  string        // destination file; if empty it is derived from the URL
	Connections int           // number of parallel segments (default 8)
	MaxRetries  int           // per-segment retry attempts on transient errors (default 5)
	RetryWait   time.Duration // base backoff between retries (default 1s, grows linearly)
	UserAgent   string        // sent as the User-Agent header
	Timeout     time.Duration // connect/response-header timeout per request (default 60s); 0 = none

	// Referer and Cookie are forwarded to the server so downloads that require a
	// referring page or an authenticated session (the usual cause of HTTP 403)
	// succeed. The browser extension fills these from the originating tab.
	Referer string
	Cookie  string
	// Headers are extra request headers applied to every segment request. They
	// override the defaults above on key collision.
	Headers map[string]string

	// MaxBytesPerSec caps total download throughput across all connections.
	// 0 means unlimited. This is a whole-download cap, not per-segment.
	MaxBytesPerSec int64

	// Limiter, when set, is used instead of MaxBytesPerSec. Sharing one limiter
	// between downloads caps their combined speed.
	Limiter *RateLimiter

	// ConnLimit, when set, is shared with other downloads from the same server
	// and caps their combined number of active connections.
	ConnLimit *ConnLimit

	// ExpectedSHA256 and ExpectedSize, when set, are verified after the download
	// completes. A mismatch makes Download return an error. Empty/0 skip the check.
	ExpectedSHA256 string
	ExpectedSize   int64
}

func (o *Options) applyDefaults() {
	if o.Connections <= 0 {
		o.Connections = 8
	}
	if o.MaxRetries <= 0 {
		o.MaxRetries = 5
	}
	if o.RetryWait <= 0 {
		o.RetryWait = time.Second
	}
	if o.UserAgent == "" {
		// Present as a normal browser by default; some servers 403 unknown agents.
		o.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) fastget/1.0"
	}
	if o.Timeout == 0 {
		// This bounds connect + response-header time, NOT the full body transfer,
		// so large segments that stream for minutes are never killed by it.
		o.Timeout = 60 * time.Second
	}
}

// Progress is a snapshot of a running download, emitted periodically to a callback.
type Progress struct {
	TotalSize   int64
	Downloaded  int64
	SpeedBps    float64 // instantaneous bytes/sec over the last sampling window
	Connections int
	Done        bool
}
