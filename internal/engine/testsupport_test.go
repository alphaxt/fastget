package engine

import (
	"bytes"
	"io"
	"net/http"
	"time"
)

// testModTime is a fixed modification time for http.ServeContent in tests.
var testModTime = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// newReadSeeker adapts a byte slice to the io.ReadSeeker that http.ServeContent
// needs (it uses Seek to satisfy Range requests).
func newReadSeeker(b []byte) io.ReadSeeker {
	return bytes.NewReader(b)
}

// countingWriter wraps a ResponseWriter to count the response-body bytes written,
// letting a test assert that resume fetched only the missing region.
type countingWriter struct {
	http.ResponseWriter
	n *int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	*c.n += int64(n)
	return n, err
}
