// Command fastget-host is the native-messaging host for the browser extension.
//
// Browsers (Chrome/Edge/Firefox) talk to a native host over stdin/stdout using a
// framed protocol: each message is a 4-byte little-endian length prefix followed
// by that many bytes of UTF-8 JSON.
//
// The host does not download anything itself. It hands each download to the
// fastget desktop app (starting it in the tray if it is not running), so every
// browser download shows up in the download list with live progress and
// pause/resume, and keeps going after the browser closes. It also shows the
// native "choose folder" dialog for the extension's confirm popup, because
// extensions cannot open one themselves.
//
// Security note: a browser only launches this host if its manifest is registered
// on the machine AND lists the specific extension ID allowed to connect.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/yourname/fastget/internal/fsutil"
	"github.com/yourname/fastget/internal/picker"
)

// uiAddr is where the fastget desktop app serves its API (its default address).
const uiAddr = "127.0.0.1:7799"

// request is what the extension sends us.
type request struct {
	// "download" | "default-folder" | "pick-folder" | "folder-info" | "open-ui" | "ping"
	Type        string `json:"type"`
	URL         string `json:"url"`
	Filename    string `json:"filename,omitempty"`
	Folder      string `json:"folder,omitempty"`   // where to save ("" = default)
	Remember    bool   `json:"remember,omitempty"` // make Folder the default
	Referrer    string `json:"referrer,omitempty"`
	Cookie      string `json:"cookie,omitempty"`
	UserAgent   string `json:"userAgent,omitempty"`
	Connections int    `json:"connections,omitempty"`
}

// response is what we send back.
type response struct {
	Type        string  `json:"type"` // "queued" | "folder" | "cancelled" | "ui" | "pong" | "error"
	URL         string  `json:"url,omitempty"`
	JobID       string  `json:"jobId,omitempty"`
	OutputPath  string  `json:"outputPath,omitempty"`
	ProgressURL string  `json:"progressUrl,omitempty"`
	ListURL     string  `json:"listUrl,omitempty"`
	Folder      string  `json:"folder,omitempty"`
	FolderSet   bool    `json:"folderSet,omitempty"` // the user chose a default folder in fastget
	FreeBytes   *uint64 `json:"freeBytes,omitempty"`
	Message     string  `json:"message,omitempty"`
}

var httpc = &http.Client{Timeout: 10 * time.Second}

func main() {
	for {
		req, err := readMessage(os.Stdin)
		if err != nil {
			return // browser closed the port (EOF) or sent garbage
		}
		writeMessage(handle(req))
	}
}

func handle(req request) response {
	switch req.Type {
	case "ping":
		return response{Type: "pong"}
	case "open-ui":
		if err := ensureUI(); err != nil {
			return errResp(err)
		}
		return response{Type: "ui", ListURL: "http://" + uiAddr + "/"}
	case "default-folder":
		return defaultFolder()
	case "pick-folder":
		return pickFolder(req.Folder)
	case "folder-info":
		return folderResp(req.Folder, false)
	case "download":
		return queueDownload(req)
	default:
		return response{Type: "error", Message: "unknown request type " + req.Type}
	}
}

func errResp(err error) response { return response{Type: "error", Message: err.Error()} }

func folderResp(dir string, set bool) response {
	r := response{Type: "folder", Folder: dir, FolderSet: set}
	if free, ok := fsutil.FreeSpace(dir); ok {
		r.FreeBytes = &free
	}
	return r
}

// defaultFolder asks the desktop app for the user's default save folder.
func defaultFolder() response {
	if err := ensureUI(); err != nil {
		return errResp(err)
	}
	var cfg struct {
		DownloadFolder    string `json:"downloadFolder"`
		DownloadFolderSet bool   `json:"downloadFolderSet"`
	}
	if err := getJSON("/api/config", &cfg); err != nil {
		return errResp(err)
	}
	if cfg.DownloadFolder == "" {
		cfg.DownloadFolder = fsutil.DownloadsDir()
	}
	return folderResp(cfg.DownloadFolder, cfg.DownloadFolderSet)
}

// pickFolder shows the Windows folder dialog.
func pickFolder(initial string) response {
	if initial == "" {
		initial = fsutil.DownloadsDir()
	}
	dir, err := picker.PickFolder(initial)
	if errors.Is(err, picker.ErrCancelled) {
		return response{Type: "cancelled"}
	}
	if err == nil {
		dir, err = fsutil.EnsureDir(dir)
	}
	if err != nil {
		return errResp(err)
	}
	return folderResp(dir, false)
}

// queueDownload adds the download to the desktop app's queue.
func queueDownload(req request) response {
	if req.URL == "" {
		return response{Type: "error", Message: "download request has no url"}
	}
	if err := ensureUI(); err != nil {
		return response{Type: "error", URL: req.URL, Message: err.Error()}
	}
	if req.Folder != "" {
		dir, err := fsutil.EnsureDir(req.Folder)
		if err != nil {
			return response{Type: "error", URL: req.URL, Message: err.Error()}
		}
		req.Folder = dir
		if req.Remember {
			if err := postJSON("/api/config", map[string]any{"downloadFolder": dir}, nil); err != nil {
				return response{Type: "error", URL: req.URL, Message: "could not save the default folder: " + err.Error()}
			}
		}
	}

	var res struct {
		ID         string `json:"id"`
		OutputPath string `json:"outputPath"`
	}
	err := postJSON("/api/jobs", map[string]any{
		"url":         req.URL,
		"folder":      req.Folder,
		"filename":    req.Filename,
		"connections": req.Connections,
		"referer":     req.Referrer,
		"cookie":      req.Cookie,
		"userAgent":   req.UserAgent,
	}, &res)
	if err != nil {
		return response{Type: "error", URL: req.URL, Message: err.Error()}
	}
	return response{
		Type:        "queued",
		URL:         req.URL,
		JobID:       res.ID,
		OutputPath:  res.OutputPath,
		ProgressURL: "http://" + uiAddr + "/progress?id=" + url.QueryEscape(res.ID),
		ListURL:     "http://" + uiAddr + "/",
	}
}

// --- talking to the desktop app ---

func getJSON(path string, out any) error {
	resp, err := httpc.Get("http://" + uiAddr + path)
	if err != nil {
		return fmt.Errorf("could not reach fastget: %w", err)
	}
	defer resp.Body.Close()
	return decode(resp, out)
}

func postJSON(path string, body, out any) error {
	b, _ := json.Marshal(body)
	r, err := http.NewRequest(http.MethodPost, "http://"+uiAddr+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := httpc.Do(r)
	if err != nil {
		return fmt.Errorf("could not reach fastget: %w", err)
	}
	defer resp.Body.Close()
	return decode(resp, out)
}

func decode(resp *http.Response, out any) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("fastget answered HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// ensureUI makes sure the fastget desktop app is running, starting it in the
// tray if needed, and waits until its API answers.
func ensureUI() error {
	if uiAlive() {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	uiName := "fastget-ui"
	if runtime.GOOS == "windows" {
		uiName += ".exe"
	}
	uiExe := filepath.Join(filepath.Dir(exe), uiName)
	if _, err := os.Stat(uiExe); err != nil {
		return fmt.Errorf("fastget app not found next to the host (%s)", uiExe)
	}
	if err := startDetached(uiExe, "--tray"); err != nil {
		return fmt.Errorf("could not start fastget: %w", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if uiAlive() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("fastget did not start in time")
}

func uiAlive() bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + uiAddr + "/api/ping")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// --- native messaging framing ---

func readMessage(r io.Reader) (request, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return request{}, err
	}
	n := binary.LittleEndian.Uint32(lenBuf[:])
	if n == 0 || n > 4*1024*1024 {
		return request{}, fmt.Errorf("invalid message length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return request{}, err
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		return request{}, err
	}
	return req, nil
}

func writeMessage(resp response) {
	body, err := json.Marshal(resp)
	if err != nil {
		return
	}
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(body)))
	_, _ = os.Stdout.Write(append(lenBuf[:], body...))
}
