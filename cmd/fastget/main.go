// Command fastget is a clean-room, multi-connection file downloader.
//
// It demonstrates the core engine: it splits a file into parallel byte-range
// segments, downloads them concurrently, supports pause/resume via a sidecar
// state file, and retries transient errors. This is the "download fast" core
// that a browser extension and desktop UI can later drive.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yourname/fastget/internal/engine"
)

func main() {
	var (
		conns   = flag.Int("c", 8, "number of parallel connections (segments)")
		out     = flag.String("o", "", "output file path (default: derived from URL)")
		retries = flag.Int("retries", 5, "per-segment retry attempts on transient errors")
		timeout = flag.Duration("timeout", 30*time.Second, "per-request timeout (0 = none)")
		ua      = flag.String("ua", "", "custom User-Agent header")
		limit   = flag.String("limit", "", "max total speed, e.g. 500k, 2m (0/empty = unlimited)")
		sha256  = flag.String("sha256", "", "expected SHA-256; download fails if the result differs")
		size    = flag.Int64("size", 0, "expected size in bytes; download fails if the result differs")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "fastget - multi-connection downloader\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n  fastget [flags] <url>\n\nFlags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample:\n  fastget -c 16 -o ubuntu.iso https://releases.ubuntu.com/.../ubuntu.iso\n")
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	url := flag.Arg(0)

	// Ctrl+C cancels cleanly; progress is persisted so a re-run resumes.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rate, err := parseRate(*limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid -limit %q: %v\n", *limit, err)
		os.Exit(2)
	}

	d := engine.New(engine.Options{
		URL:            url,
		OutputPath:     *out,
		Connections:    *conns,
		MaxRetries:     *retries,
		Timeout:        *timeout,
		UserAgent:      *ua,
		MaxBytesPerSec: rate,
		ExpectedSHA256: *sha256,
		ExpectedSize:   *size,
	})

	start := time.Now()
	err = d.Download(ctx, func(p engine.Progress) {
		printProgress(p)
	})
	fmt.Println() // finish the progress line

	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "\ninterrupted; progress saved. Re-run the same command to resume.\n")
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("done in %s\n", time.Since(start).Round(time.Millisecond))
}

// printProgress renders a single-line progress bar with speed and percentage.
func printProgress(p engine.Progress) {
	const barWidth = 30
	if p.TotalSize <= 0 {
		// Unknown size: just show bytes and speed.
		fmt.Printf("\r%s  %s/s        ", humanBytes(p.Downloaded), humanBytes(int64(p.SpeedBps)))
		return
	}
	ratio := float64(p.Downloaded) / float64(p.TotalSize)
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio * barWidth)
	bar := ""
	for i := 0; i < barWidth; i++ {
		if i < filled {
			bar += "="
		} else {
			bar += " "
		}
	}
	fmt.Printf("\r[%s] %5.1f%%  %s/%s  %s/s  (%d conns)   ",
		bar, ratio*100,
		humanBytes(p.Downloaded), humanBytes(p.TotalSize),
		humanBytes(int64(p.SpeedBps)), p.Connections)
}

// humanBytes formats a byte count in binary units (KiB, MiB, ...).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// parseRate converts a human rate string into bytes/sec. Accepts a plain number
// (bytes) or a suffix: k/kb (KiB), m/mb (MiB), g/gb (GiB). Empty or "0" means
// unlimited (returns 0).
func parseRate(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "0" {
		return 0, nil
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "gb"):
		mult, s = 1024*1024*1024, strings.TrimSuffix(s, "gb")
	case strings.HasSuffix(s, "g"):
		mult, s = 1024*1024*1024, strings.TrimSuffix(s, "g")
	case strings.HasSuffix(s, "mb"):
		mult, s = 1024*1024, strings.TrimSuffix(s, "mb")
	case strings.HasSuffix(s, "m"):
		mult, s = 1024*1024, strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "kb"):
		mult, s = 1024, strings.TrimSuffix(s, "kb")
	case strings.HasSuffix(s, "k"):
		mult, s = 1024, strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "b"):
		mult, s = 1, strings.TrimSuffix(s, "b")
	}
	s = strings.TrimSpace(s)
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("not a number")
	}
	if val < 0 {
		return 0, fmt.Errorf("must be >= 0")
	}
	return int64(val * float64(mult)), nil
}
