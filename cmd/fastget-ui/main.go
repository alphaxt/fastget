// Command fastget-ui is the fastget desktop download manager.
//
// It runs the download queue, serves the download list and per-download progress
// windows on loopback, and sits in the system tray (like IDM) until you choose
// Quit. Only one copy runs at a time: launching it again just opens the list of
// the copy that is already running.
//
// The browser extension's native host starts this app automatically (in --tray
// mode) when it needs to hand over a download.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/alphaxt/fastget/internal/queue"
	"github.com/alphaxt/fastget/internal/tray"
	"github.com/alphaxt/fastget/internal/ui"
)

func main() {
	var (
		addr      = flag.String("addr", "127.0.0.1:7799", "address to bind the UI server (loopback recommended)")
		maxConc   = flag.Int("parallel", 3, "default number of downloads to run at once")
		statePath = flag.String("state", defaultStatePath(), "path to the persisted queue file")
		noOpen    = flag.Bool("no-open", false, "do not open the download list on start")
		trayMode  = flag.Bool("tray", false, "start quietly in the system tray (used for auto-start and by the browser extension)")
	)
	flag.Parse()

	// Bind first: if another fastget is already serving this address, hand off to
	// it instead of starting a second queue on the same state file.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		if isFastget(*addr) {
			if !*noOpen && !*trayMode {
				openBrowser("http://" + *addr + "/")
			}
			return
		}
		fmt.Fprintf(os.Stderr, "cannot bind %s: %v\n", *addr, err)
		os.Exit(1)
	}

	mgr, err := queue.NewManager(*statePath, *maxConc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start queue: %v\n", err)
		os.Exit(1)
	}

	srv := ui.NewServer(mgr)
	httpSrv := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	url := "http://" + ln.Addr().String() + "/"
	fmt.Printf("fastget running at %s  (%s)\n", url, srv.Describe())

	serveErr := make(chan error, 1)
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	if !*noOpen && !*trayMode {
		openBrowser(url)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The tray owns the main thread until the user picks Quit, a termination
	// signal arrives, or the server dies. On non-Windows systems it just waits.
	trayCtx, trayCancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-ctx.Done():
		case err := <-serveErr:
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		}
		trayCancel()
	}()
	_ = tray.Run(tray.Options{
		Title:   "fastget download manager",
		Context: trayCtx,
		OnOpen:  func() { openBrowser(url) },
	})

	// Graceful: stop accepting, then shut the queue (pauses running downloads).
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	mgr.Shutdown()
}

// isFastget reports whether a fastget server is already answering at addr.
func isFastget(addr string) bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + addr + "/api/ping")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// defaultStatePath puts the queue file in a per-user app data dir.
func defaultStatePath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "fastget", "queue.json")
	}
	return "queue.json"
}

// openBrowser launches the default browser at url, best-effort per OS.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
