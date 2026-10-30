package ui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourname/fastget/internal/queue"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	m, err := queue.NewManager(filepath.Join(t.TempDir(), "q.json"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)
	return NewServer(m).Handler()
}

func do(h http.Handler, method, target, host, ctype, origin, body string) int {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = host
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestPagesAndGuard(t *testing.T) {
	h := newTestServer(t)
	const local = "127.0.0.1:7799"
	add := `{"url":"https://example.com/a.bin","outputPath":"` + strings.ReplaceAll(filepath.Join(t.TempDir(), "a.bin"), `\`, `\\`) + `"}`

	cases := []struct {
		name                         string
		method, target, host, ct, or string
		body                         string
		want                         int
	}{
		{"list page", "GET", "/", local, "", "", "", 200},
		{"progress page", "GET", "/progress?id=x", local, "", "", "", 200},
		{"ping", "GET", "/api/ping", local, "", "", "", 200},
		{"unknown job", "GET", "/api/jobs/nope", local, "", "", "", 404},
		{"foreign host (DNS rebinding)", "GET", "/api/jobs", "evil.example:7799", "", "", "", 403},
		{"cross-site form post", "POST", "/api/jobs", local, "text/plain", "", add, 415},
		{"cross-site json post", "POST", "/api/jobs", local, "application/json", "https://evil.example", add, 403},
		{"local add", "POST", "/api/jobs", local, "application/json", "http://" + local, add, 200},
	}
	for _, c := range cases {
		if got := do(h, c.method, c.target, c.host, c.ct, c.or, c.body); got != c.want {
			t.Errorf("%s: got HTTP %d, want %d", c.name, got, c.want)
		}
	}
}

// TestSaveFolder: a download sent with a folder is saved in that folder (not
// Downloads); the default folder setting is used when none is given; a second
// download of the same name does not overwrite the first.
func TestSaveFolder(t *testing.T) {
	h := newTestServer(t)
	const local = "127.0.0.1:7799"
	esc := func(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }
	post := func(path, body string) (int, string) {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Host = local
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	chosen := filepath.Join(t.TempDir(), "H-drive")
	code, body := post("/api/jobs", `{"url":"https://example.com/TR2013PG.rar","folder":"`+esc(chosen)+`","filename":"TR2013PG.rar"}`)
	if code != 200 || !strings.Contains(body, esc(filepath.Join(chosen, "TR2013PG.rar"))) {
		t.Fatalf("chosen folder ignored: HTTP %d %s", code, body)
	}

	def := filepath.Join(t.TempDir(), "default")
	if code, body := post("/api/config", `{"downloadFolder":"`+esc(def)+`"}`); code != 200 {
		t.Fatalf("set default folder: HTTP %d %s", code, body)
	}
	code, body = post("/api/jobs", `{"url":"https://example.com/a.zip"}`)
	if code != 200 || !strings.Contains(body, esc(filepath.Join(def, "a.zip"))) {
		t.Fatalf("default folder ignored: HTTP %d %s", code, body)
	}

	// Simulate an unfinished download already on disk with the same name.
	os.WriteFile(filepath.Join(def, "a.zip"), []byte("x"), 0o644)
	code, body = post("/api/jobs", `{"url":"https://example.com/a.zip"}`)
	if code != 200 || !strings.Contains(body, esc(filepath.Join(def, "a (1).zip"))) {
		t.Fatalf("existing file would be overwritten: HTTP %d %s", code, body)
	}
}
