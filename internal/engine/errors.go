package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/alphaxt/fastget/internal/fsutil"
)

// stallTimeout is how long one connection may go without receiving any data
// before it is dropped and retried from where it stopped.
var stallTimeout = 30 * time.Second

// errYield means a connection was handed to another download from the same
// server; the unfinished piece goes back to the queue.
var errYield = errors.New("connection handed to another download")

// errStalled marks a connection that stopped sending data.
var errStalled = errors.New("connection stalled (no data received)")

// statusError is an HTTP error status returned by the server.
type statusError struct{ code int }

func (e *statusError) Error() string {
	switch e.code {
	case http.StatusForbidden:
		return "server refused access (HTTP 403). The link may have expired or need the page's login; start the download again from the browser"
	case http.StatusUnauthorized:
		return "server requires a login (HTTP 401)"
	case http.StatusNotFound:
		return "file not found on the server (HTTP 404)"
	case http.StatusGone:
		return "the link has expired (HTTP 410)"
	case http.StatusRequestedRangeNotSatisfiable:
		return "server rejected the byte range (HTTP 416)"
	case http.StatusTooManyRequests:
		return "server is rate-limiting this download (HTTP 429)"
	}
	return fmt.Sprintf("server returned HTTP %d %s", e.code, http.StatusText(e.code))
}

// diskSpaceError means the destination drive is (or would become) full.
type diskSpaceError struct {
	path       string
	need, free uint64 // need is 0 when the disk filled up mid-download
}

func (e *diskSpaceError) Error() string {
	drive := fsutil.DriveOf(e.path)
	if e.need > 0 {
		return fmt.Sprintf("not enough free space on %s: the file needs %s but only %s is free. Use \"Change folder\" to save it on another drive",
			drive, fsutil.HumanBytes(int64(e.need)), fsutil.HumanBytes(int64(e.free)))
	}
	return fmt.Sprintf("drive %s is full (%s free). Free up space or use \"Change folder\" to save it on another drive",
		drive, fsutil.HumanBytes(int64(e.free)))
}

// isPermanent reports whether retrying cannot help: a full disk, and client
// errors (4xx) other than timeouts and rate limiting. Network errors, stalls
// and 5xx are retried.
func isPermanent(err error) bool {
	var de *diskSpaceError
	if errors.As(err, &de) {
		return true
	}
	var se *statusError
	if errors.As(err, &se) {
		if se.code == http.StatusRequestTimeout || se.code == http.StatusTooManyRequests {
			return false
		}
		return se.code >= 400 && se.code < 500
	}
	return false
}

// stalledOr turns a cancellation caused by the stall watchdog (rather than by
// the user) into errStalled so the caller retries it.
func stalledOr(parent, req context.Context, err error) error {
	if parent.Err() == nil && req.Err() != nil {
		return errStalled
	}
	return err
}
