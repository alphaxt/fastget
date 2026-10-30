// Package queue manages multiple downloads: a persistent job list and a scheduler
// that runs up to a configurable number of them concurrently. It wraps the engine
// package, which handles the actual multi-connection download and byte-level
// resume. The queue adds the "download list" layer: add, pause, resume, cancel,
// reorder, and survive restarts.
package queue

import (
	"time"

	"github.com/yourname/fastget/internal/engine"
)

// Status is the lifecycle state of a job.
type Status string

const (
	StatusQueued    Status = "queued"    // waiting for a free worker slot
	StatusRunning   Status = "running"   // actively downloading
	StatusPaused    Status = "paused"    // user-paused; resumable sidecar kept
	StatusDone      Status = "done"      // completed successfully
	StatusError     Status = "error"     // failed; LastError has the reason
	StatusScheduled Status = "scheduled" // waiting for its StartAt time (or the allowed window)
)

// terminal reports whether a status will not change without user action.
func (s Status) terminal() bool {
	return s == StatusDone
}

// Job is one download tracked by the queue. Mutable fields are guarded by the
// Manager's mutex; callers interact through Manager methods, not Job fields.
type Job struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	OutputPath  string `json:"output_path"`
	Connections int    `json:"connections"`
	UserAgent   string `json:"user_agent,omitempty"`

	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	ExpectedSize   int64  `json:"expected_size,omitempty"`

	// Referer and Cookie come from the browser tab that started the download, so
	// session- or referrer-protected downloads keep working (and can resume).
	Referer string `json:"referer,omitempty"`
	Cookie  string `json:"cookie,omitempty"`

	Status     Status `json:"status"`
	TotalSize  int64  `json:"total_size"`
	Downloaded int64  `json:"downloaded"`
	LastError  string `json:"last_error,omitempty"`

	AddedAt     time.Time `json:"added_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`

	// StartAt, when non-zero, delays the job until this wall-clock time. Until
	// then the job sits in StatusScheduled. Zero means start as soon as a slot is
	// free.
	StartAt time.Time `json:"start_at,omitempty"`

	// Runtime-only fields (not persisted): the cancel func for the running
	// download, a live speed sample, and whether the most recent pause was caused
	// by the allowed-time window closing (so it auto-resumes when it reopens).
	cancel       func() `json:"-"`
	speedBps     float64
	windowPaused bool
}

// snapshotLocked returns a copy of the job's public fields. Caller holds the lock.
func (j *Job) snapshotLocked() JobView {
	var pct float64
	if j.TotalSize > 0 {
		pct = float64(j.Downloaded) / float64(j.TotalSize) * 100
	}
	return JobView{
		ID:          j.ID,
		URL:         j.URL,
		OutputPath:  j.OutputPath,
		Connections: j.Connections,
		Status:      j.Status,
		TotalSize:   j.TotalSize,
		Downloaded:  j.Downloaded,
		Percent:     pct,
		SpeedBps:    j.speedBps,
		LastError:   j.LastError,
		AddedAt:     j.AddedAt,
		CompletedAt: j.CompletedAt,
		StartAt:     j.StartAt,
	}
}

// JobView is an immutable snapshot of a job for reading (UI, API). It never shares
// mutable state with the Manager.
type JobView struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	OutputPath  string    `json:"output_path"`
	Connections int       `json:"connections"`
	Status      Status    `json:"status"`
	TotalSize   int64     `json:"total_size"`
	Downloaded  int64     `json:"downloaded"`
	Percent     float64   `json:"percent"`
	SpeedBps    float64   `json:"speed_bps"`
	LastError   string    `json:"last_error,omitempty"`
	AddedAt     time.Time `json:"added_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	StartAt     time.Time `json:"start_at,omitempty"`
}

// toEngineOptions builds engine options from a job. The manager supplies the
// shared total-throughput cap (applied per running job).
func (j *Job) toEngineOptions(maxBytesPerSec int64) engine.Options {
	return engine.Options{
		URL:            j.URL,
		OutputPath:     j.OutputPath,
		Connections:    j.Connections,
		UserAgent:      j.UserAgent,
		Referer:        j.Referer,
		Cookie:         j.Cookie,
		MaxBytesPerSec: maxBytesPerSec,
		ExpectedSHA256: j.ExpectedSHA256,
		ExpectedSize:   j.ExpectedSize,
	}
}

// TimeWindow is an optional "only download between these times of day" schedule,
// like a classic download manager's scheduler. Start and End are minutes since
// midnight (0..1439) in local time. When Enabled is false the window is ignored.
// A window where End <= Start wraps past midnight (e.g. 22:00->06:00).
type TimeWindow struct {
	Enabled bool `json:"enabled"`
	Start   int  `json:"start_min"` // minutes since midnight, inclusive
	End     int  `json:"end_min"`   // minutes since midnight, exclusive
}

// contains reports whether the given local time falls inside the window.
func (w TimeWindow) contains(t time.Time) bool {
	if !w.Enabled {
		return true // no window configured means "always allowed"
	}
	mins := t.Hour()*60 + t.Minute()
	if w.Start == w.End {
		return true // degenerate full-day window
	}
	if w.Start < w.End {
		return mins >= w.Start && mins < w.End
	}
	// Wrapping window (e.g. 22:00 -> 06:00).
	return mins >= w.Start || mins < w.End
}

// persistState is the on-disk shape of the whole queue.
type persistState struct {
	Jobs           []*Job     `json:"jobs"`
	MaxConcurrent  int        `json:"max_concurrent"`
	MaxBytesPerSec int64      `json:"max_bytes_per_sec,omitempty"`
	Window         TimeWindow `json:"window,omitempty"`
	DownloadFolder string     `json:"download_folder,omitempty"`
}
