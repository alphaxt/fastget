// Package ui serves a local web interface for the download queue. It runs an HTTP
// server bound to loopback and exposes a small JSON API that the embedded pages
// poll and control: the download list ("/") and a per-download progress window
// ("/progress?id=...") that the browser extension opens, IDM-style.
package ui

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yourname/fastget/internal/fsutil"
	"github.com/yourname/fastget/internal/picker"
	"github.com/yourname/fastget/internal/queue"
)

//go:embed static/*
var staticFiles embed.FS

// Server wraps a queue.Manager with an HTTP handler.
type Server struct {
	mgr *queue.Manager
	mux *http.ServeMux
}

// NewServer builds the HTTP server around a queue manager.
func NewServer(mgr *queue.Manager) *Server {
	s := &Server{mgr: mgr, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler exposes the router wrapped in the local-only request guard.
func (s *Server) Handler() http.Handler { return guard(s.mux) }

func (s *Server) routes() {
	sub, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(sub))
	s.mux.Handle("/static/", http.StripPrefix("/static/", fileServer))

	s.mux.HandleFunc("/", s.page("static/index.html"))
	s.mux.HandleFunc("/progress", s.page("static/progress.html"))
	s.mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"app": "fastget", "ok": true})
	})
	s.mux.HandleFunc("/api/jobs", s.handleJobs)       // GET list, POST add
	s.mux.HandleFunc("/api/jobs/", s.handleJobAction) // GET /api/jobs/{id}, POST /api/jobs/{id}/{action}
	s.mux.HandleFunc("/api/config", s.handleConfig)   // GET/POST settings
	s.mux.HandleFunc("/api/pick-folder", s.handlePickFolder)
	s.mux.HandleFunc("/api/folder-info", s.handleFolderInfo)
}

// saveFolder returns the folder new downloads go to when none is given.
func (s *Server) saveFolder() string {
	if d := s.mgr.DownloadFolder(); d != "" {
		return d
	}
	return fsutil.DownloadsDir()
}

// folderInfo describes a folder for the UI: its path and the free space on it.
func folderInfo(dir string) map[string]any {
	info := map[string]any{"folder": dir}
	if free, ok := fsutil.FreeSpace(dir); ok {
		info["freeBytes"] = free
	}
	return info
}

// handlePickFolder shows the native folder dialog (POST {"initial": "..."}).
func (s *Server) handlePickFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Initial string `json:"initial"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Initial == "" {
		req.Initial = s.saveFolder()
	}
	dir, err := picker.PickFolder(req.Initial)
	if errors.Is(err, picker.ErrCancelled) {
		writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
		return
	}
	if err == nil {
		dir, err = fsutil.EnsureDir(dir)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, folderInfo(dir))
}

// handleFolderInfo reports free space for GET /api/folder-info?dir=...
func (s *Server) handleFolderInfo(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = s.saveFolder()
	}
	writeJSON(w, http.StatusOK, folderInfo(dir))
}

// guard rejects requests that did not come from this machine's own pages or
// local programs. The API listens on loopback, but any website open in the
// browser could still try to reach it, so we:
//   - accept only loopback Host headers (blocks DNS-rebinding attacks), and
//   - for state-changing requests, require a JSON content type (cross-site
//     pages cannot send that without a CORS preflight, which we never grant)
//     and, if the browser sent an Origin, require it to be this server.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mt != "application/json" {
				http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// page serves one embedded HTML page at an exact path.
func (s *Server) page(file string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/progress" {
			http.NotFound(w, r)
			return
		}
		b, err := staticFiles.ReadFile(file)
		if err != nil {
			http.Error(w, "page not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	}
}

// addRequest is the body for POST /api/jobs.
type addRequest struct {
	URL            string `json:"url"`
	OutputPath     string `json:"outputPath"`
	Connections    int    `json:"connections"`
	ExpectedSHA256 string `json:"expectedSha256,omitempty"`
	ExpectedSize   int64  `json:"expectedSize,omitempty"`
	// StartAt is an optional RFC3339 timestamp; the job waits until then.
	StartAt string `json:"startAt,omitempty"`
	// Browser context forwarded by the extension/native host.
	Referer   string `json:"referer,omitempty"`
	Cookie    string `json:"cookie,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	// When OutputPath is empty, the file is saved as Filename (or a name taken
	// from the URL) inside Folder (or the default save folder).
	Folder   string `json:"folder,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// resolveOutput decides the full save path for a new download.
func (s *Server) resolveOutput(req addRequest) (string, error) {
	if strings.TrimSpace(req.OutputPath) != "" {
		if !filepath.IsAbs(req.OutputPath) {
			return "", fmt.Errorf("outputPath must be a full path")
		}
		dir, err := fsutil.EnsureDir(filepath.Dir(req.OutputPath))
		if err != nil {
			return "", err
		}
		return fsutil.UniquePath(filepath.Join(dir, fsutil.SafeName(filepath.Base(req.OutputPath), req.URL))), nil
	}
	folder := req.Folder
	if strings.TrimSpace(folder) == "" {
		folder = s.saveFolder()
	}
	dir, err := fsutil.EnsureDir(folder)
	if err != nil {
		return "", err
	}
	return fsutil.UniquePath(filepath.Join(dir, fsutil.SafeName(req.Filename, req.URL))), nil
}

// handleJobs lists jobs (GET) or adds one (POST).
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"jobs": s.mgr.List()})
	case http.MethodPost:
		var req addRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
			return
		}
		if strings.TrimSpace(req.URL) == "" {
			writeJSON(w, http.StatusBadRequest, errBody("url is required"))
			return
		}
		var startAt time.Time
		if strings.TrimSpace(req.StartAt) != "" {
			parsed, perr := time.Parse(time.RFC3339, req.StartAt)
			if perr != nil {
				writeJSON(w, http.StatusBadRequest, errBody("startAt must be RFC3339, e.g. 2026-01-02T15:04:05Z"))
				return
			}
			startAt = parsed
		}
		out, err := s.resolveOutput(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errBody(err.Error()))
			return
		}
		id, err := s.mgr.Add(queue.AddOptions{
			URL:            req.URL,
			OutputPath:     out,
			Connections:    req.Connections,
			UserAgent:      req.UserAgent,
			ExpectedSHA256: req.ExpectedSHA256,
			ExpectedSize:   req.ExpectedSize,
			StartAt:        startAt,
			Referer:        req.Referer,
			Cookie:         req.Cookie,
		})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errBody(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "outputPath": out})
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleJobAction handles GET /api/jobs/{id} (one job, for the progress window)
// and POST /api/jobs/{id}/{pause|resume|remove|remove-file|open|open-folder}.
func (s *Server) handleJobAction(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(path.Clean("/"+strings.TrimPrefix(r.URL.Path, "/api/jobs/")), "/")
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		writeJSON(w, http.StatusBadRequest, errBody("missing job id"))
		return
	}

	if r.Method == http.MethodGet && len(parts) == 1 {
		v, ok := s.mgr.Get(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, errBody("no such job"))
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	}
	if r.Method != http.MethodPost || len(parts) != 2 {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	action := parts[1]
	var err error
	switch action {
	case "pause":
		err = s.mgr.Pause(id)
	case "resume":
		err = s.mgr.Resume(id)
	case "remove":
		err = s.mgr.Remove(id, false)
	case "remove-file":
		err = s.mgr.Remove(id, true)
	case "move":
		s.moveJob(w, r, id)
		return
	case "open", "open-folder":
		v, ok := s.mgr.Get(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, errBody("no such job"))
			return
		}
		if action == "open" && v.Status != queue.StatusDone {
			writeJSON(w, http.StatusConflict, errBody("the download has not finished yet"))
			return
		}
		err = reveal(v.OutputPath, action == "open")
	default:
		writeJSON(w, http.StatusBadRequest, errBody("unknown action "+action))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusNotFound, errBody(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// moveJob handles POST /api/jobs/{id}/move {"folder": "..."}. With no folder it
// shows the folder dialog first.
func (s *Server) moveJob(w http.ResponseWriter, r *http.Request, id string) {
	v, ok := s.mgr.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, errBody("no such job"))
		return
	}
	var req struct {
		Folder string `json:"folder"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	folder := req.Folder
	if strings.TrimSpace(folder) == "" {
		initial := s.saveFolder()
		if v.OutputPath != "" {
			initial = filepath.Dir(v.OutputPath)
		}
		picked, err := picker.PickFolder(initial)
		if errors.Is(err, picker.ErrCancelled) {
			writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errBody(err.Error()))
			return
		}
		folder = picked
	}
	dir, err := fsutil.EnsureDir(folder)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err.Error()))
		return
	}
	name := filepath.Base(v.OutputPath)
	if v.OutputPath == "" {
		name = fsutil.SafeName("", v.URL)
	}
	target := filepath.Join(dir, name)
	if !strings.EqualFold(target, v.OutputPath) {
		target = fsutil.UniquePath(target)
	}
	if err := s.mgr.Move(id, target); err != nil {
		writeJSON(w, http.StatusConflict, errBody(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "outputPath": target})
}

// reveal opens a downloaded file (openFile=true) or shows it in its folder.
// The path always comes from the job record, never from the request.
func reveal(p string, openFile bool) error {
	if p == "" {
		return fmt.Errorf("download has no file path yet")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		if openFile {
			return fmt.Errorf("file not found: %s", abs)
		}
		abs = filepath.Dir(abs) // file gone: still open its folder
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		if openFile {
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", abs)
		} else {
			cmd = exec.Command("explorer", "/select,"+abs)
		}
	case "darwin":
		if openFile {
			cmd = exec.Command("open", abs)
		} else {
			cmd = exec.Command("open", "-R", abs)
		}
	default:
		target := abs
		if !openFile {
			target = filepath.Dir(abs)
		}
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

// windowConfig is the JSON shape of the allowed-time window in the API.
type windowConfig struct {
	Enabled  bool `json:"enabled"`
	StartMin int  `json:"startMin"`
	EndMin   int  `json:"endMin"`
}

// configRequest is the body for POST /api/config. Fields are optional; only the
// ones present in the request are applied.
type configRequest struct {
	MaxConcurrent  *int          `json:"maxConcurrent,omitempty"`
	MaxBytesPerSec *int64        `json:"maxBytesPerSec,omitempty"`
	Window         *windowConfig `json:"window,omitempty"`
	DownloadFolder *string       `json:"downloadFolder,omitempty"` // "" resets to Downloads
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		win := s.mgr.Window()
		folder := s.saveFolder()
		cfg := map[string]any{
			"maxConcurrent":     s.mgr.MaxConcurrent(),
			"maxBytesPerSec":    s.mgr.MaxBytesPerSec(),
			"window":            windowConfig{Enabled: win.Enabled, StartMin: win.Start, EndMin: win.End},
			"downloadFolder":    folder,
			"downloadFolderSet": s.mgr.DownloadFolder() != "",
		}
		if free, ok := fsutil.FreeSpace(folder); ok {
			cfg["freeBytes"] = free
		}
		writeJSON(w, http.StatusOK, cfg)
	case http.MethodPost:
		var req configRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
			return
		}
		if req.MaxConcurrent != nil && *req.MaxConcurrent >= 1 {
			s.mgr.SetMaxConcurrent(*req.MaxConcurrent)
		}
		if req.MaxBytesPerSec != nil && *req.MaxBytesPerSec >= 0 {
			s.mgr.SetMaxBytesPerSec(*req.MaxBytesPerSec)
		}
		if req.DownloadFolder != nil {
			if strings.TrimSpace(*req.DownloadFolder) == "" {
				s.mgr.SetDownloadFolder("")
			} else {
				dir, err := fsutil.EnsureDir(*req.DownloadFolder)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, errBody(err.Error()))
					return
				}
				s.mgr.SetDownloadFolder(dir)
			}
		}
		if req.Window != nil {
			s.mgr.SetWindow(queue.TimeWindow{
				Enabled: req.Window.Enabled,
				Start:   clampMin(req.Window.StartMin),
				End:     clampMin(req.Window.EndMin),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// clampMin keeps a minutes-since-midnight value in [0, 1439].
func clampMin(m int) int {
	if m < 0 {
		return 0
	}
	if m > 1439 {
		return 1439
	}
	return m
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errBody(msg string) map[string]any {
	return map[string]any{"error": msg}
}

// Describe returns a short human string about the server's queue (used on start).
func (s *Server) Describe() string {
	return fmt.Sprintf("%d job(s) in queue", len(s.mgr.List()))
}
