package queue

import (
	"bytes"
	"io"
	"net/http/httptest"
	"testing"
)

// newReader adapts a byte slice to the io.ReadSeeker that http.ServeContent needs
// to satisfy Range requests in tests.
func newReader(b []byte) io.ReadSeeker {
	return bytes.NewReader(b)
}

// rangeServer2 is a convenience wrapper around rangeServer with no concurrency
// instrumentation, used by scheduling tests.
func rangeServer2(t *testing.T, size int) *httptest.Server {
	t.Helper()
	return rangeServer(t, size, nil, nil)
}
