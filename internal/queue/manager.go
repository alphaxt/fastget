package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yourname/fastget/internal/engine"
)

// Manager owns the job list and schedules downloads so that at most
// MaxConcurrent run at once. It is safe for concurrent use.
//
// Concurrency model:
//   - All job-list mutations happen under mu.
//   - A background scheduler loop wakes on a channel whenever something changes
//     (a job added, finished, paused, or the concurrency limit changed) and
//     starts queued jobs until the running count hits the limit.
//   - Each running job executes in its own goroutine with its own cancellable
//     context. Pausing or cancelling calls that context's cancel func.
type Manager struct {
	mu             sync.Mutex
	jobs           []*Job
	byID           map[string]*Job
	maxConcurrent  int
	maxBytesPerSec int64               // cap on the combined speed of all downloads; 0 = unlimited
	limiter        *engine.RateLimiter // shared by every running download

	connMu         sync.Mutex
	connLimits     map[string]*engine.ConnLimit // per server
	window         TimeWindow                   // optional allowed time-of-day window
	downloadFolder string                       // user's default save folder; "" = Downloads
	running        int

	statePath string        // where the job list is persisted
	wake      chan struct{} // nudges the scheduler to re-evaluate
	stop      chan struct{} // closed on Shutdown
	stopped   bool
	wg        sync.WaitGroup // tracks running download goroutines

	// now returns the current time; overridable in tests. Defaults to time.Now.
	now func() time.Time
}

// NewManager creates a manager that persists its job list to statePath. If the
// file exists, the previous job list is loaded (running jobs become paused so the
// user decides whether to resume).
func NewManager(statePath string, maxConcurrent int) (*Manager, error) {
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	m := &Manager{
		byID:          map[string]*Job{},
		maxConcurrent: maxConcurrent,
		statePath:     statePath,
		wake:          make(chan struct{}, 1),
		stop:          make(chan struct{}),
		now:           time.Now,
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	m.limiter = engine.NewRateLimiter(m.maxBytesPerSec)
	go m.scheduler()
	return m, nil
}

// AddOptions carries the optional fields for a new job. URL is required.
type AddOptions struct {
	URL            string
	OutputPath     string
	Connections    int
	UserAgent      string
	ExpectedSHA256 string
	ExpectedSize   int64
	StartAt        time.Time // zero = start immediately; future = schedule
	Referer        string
	Cookie         string
}

// Add enqueues a new download and returns its job ID. The scheduler starts it as
// soon as a worker slot is free.
func (m *Manager) Add(opts AddOptions) (string, error) {
	if opts.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	status := StatusQueued
	if !opts.StartAt.IsZero() && opts.StartAt.After(m.now()) {
		status = StatusScheduled
	}
	j := &Job{
		ID:             newID(),
		URL:            opts.URL,
		OutputPath:     opts.OutputPath,
		Connections:    opts.Connections,
		UserAgent:      opts.UserAgent,
		ExpectedSHA256: opts.ExpectedSHA256,
		ExpectedSize:   opts.ExpectedSize,
		StartAt:        opts.StartAt,
		Referer:        opts.Referer,
		Cookie:         opts.Cookie,
		Status:         status,
		AddedAt:        m.now(),
	}
	m.jobs = append(m.jobs, j)
	m.byID[j.ID] = j
	m.persistLocked()
	m.nudge()
	return j.ID, nil
}

// Pause stops a running or queued job. A running download is cancelled but its
// byte-level resume sidecar is kept, so Resume continues where it left off.
func (m *Manager) Pause(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("no such job %q", id)
	}
	j.windowPaused = false // an explicit pause is not a window pause
	switch j.Status {
	case StatusRunning:
		if j.cancel != nil {
			j.cancel() // the goroutine will observe context cancel and set paused
		}
	case StatusQueued, StatusScheduled:
		j.Status = StatusPaused
	}
	m.persistLocked()
	return nil
}

// Resume re-queues a paused or errored job.
func (m *Manager) Resume(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("no such job %q", id)
	}
	if j.Status == StatusPaused || j.Status == StatusError {
		j.Status = StatusQueued
		j.LastError = ""
		m.persistLocked()
		m.nudge()
	}
	return nil
}

// Remove cancels (if running) and deletes a job from the list. If deleteFile is
// true, the partial output and its resume sidecar are removed from disk too.
func (m *Manager) Remove(id string, deleteFile bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("no such job %q", id)
	}
	if j.Status == StatusRunning && j.cancel != nil {
		j.cancel()
	}
	delete(m.byID, id)
	for i, x := range m.jobs {
		if x.ID == id {
			m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
			break
		}
	}
	if deleteFile && j.OutputPath != "" {
		_ = os.Remove(j.OutputPath)
		_ = os.Remove(j.OutputPath + ".fgdl")
	}
	m.persistLocked()
	return nil
}

// SetMaxConcurrent changes how many downloads may run in parallel.
func (m *Manager) SetMaxConcurrent(n int) {
	if n < 1 {
		n = 1
	}
	m.mu.Lock()
	m.maxConcurrent = n
	m.persistLocked()
	m.mu.Unlock()
	m.nudge()
}

// SetMaxBytesPerSec caps the combined speed of all downloads (0 = unlimited).
// It applies immediately, including to downloads already running.
func (m *Manager) SetMaxBytesPerSec(n int64) {
	if n < 0 {
		n = 0
	}
	m.mu.Lock()
	m.maxBytesPerSec = n
	m.persistLocked()
	m.mu.Unlock()
	m.limiter.SetRate(n)
}

// SetWindow sets the allowed download time-of-day window. Start/End are minutes
// since midnight (local). Disabling it (Enabled=false) means downloads may run at
// any time. The change is applied on the next scheduler tick.
func (m *Manager) SetWindow(w TimeWindow) {
	m.mu.Lock()
	m.window = w
	m.persistLocked()
	m.mu.Unlock()
	m.nudge()
}

// PerServerConnections caps the combined connections of all downloads from
// one server. 8 matches a single download's default, so one download runs at
// full width and several from the same server share those 8 fairly.
const PerServerConnections = 8

// connLimitFor returns the connection limit shared by all downloads from the
// same server as rawURL.
func (m *Manager) connLimitFor(rawURL string) *engine.ConnLimit {
	host := rawURL
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		host = strings.ToLower(u.Hostname())
		// download1.example.com and download2.example.com are usually the same
		// operator (MediaFire, SourceForge mirrors...): group by the last two
		// labels.
		if parts := strings.Split(host, "."); len(parts) > 2 && net.ParseIP(host) == nil {
			host = strings.Join(parts[len(parts)-2:], ".")
		}
	}
	m.connMu.Lock()
	defer m.connMu.Unlock()
	if m.connLimits == nil {
		m.connLimits = map[string]*engine.ConnLimit{}
	}
	c, ok := m.connLimits[host]
	if !ok {
		c = engine.NewConnLimit(PerServerConnections)
		m.connLimits[host] = c
	}
	return c
}

// MaxConcurrent returns how many downloads may run at once.
func (m *Manager) MaxConcurrent() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxConcurrent
}

// MaxBytesPerSec returns the combined speed limit (0 = unlimited).
func (m *Manager) MaxBytesPerSec() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxBytesPerSec
}

// SetDownloadFolder sets the default folder for new downloads ("" = Downloads).
func (m *Manager) SetDownloadFolder(dir string) {
	m.mu.Lock()
	m.downloadFolder = dir
	m.persistLocked()
	m.mu.Unlock()
}

// DownloadFolder returns the user's default folder, or "" if none was chosen.
func (m *Manager) DownloadFolder() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.downloadFolder
}

// Move changes where an unfinished download is saved and queues it again.
// The partial file and its resume data move with it when possible; if the new
// folder is on another drive the download restarts from the beginning there.
func (m *Manager) Move(id, newPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("no such job %q", id)
	}
	switch j.Status {
	case StatusRunning:
		return fmt.Errorf("pause the download before changing its folder")
	case StatusDone:
		return fmt.Errorf("the download has already finished")
	}
	old := j.OutputPath
	if old != "" && old != newPath {
		if _, err := os.Stat(old); err == nil {
			if err := os.Rename(old, newPath); err == nil {
				_ = os.Rename(old+".fgdl", newPath+".fgdl")
			} else {
				// Different drive: start over in the new place.
				_ = os.Remove(old)
				_ = os.Remove(old + ".fgdl")
				j.Downloaded = 0
			}
		} else {
			_ = os.Remove(old + ".fgdl")
			j.Downloaded = 0
		}
	}
	j.OutputPath = newPath
	j.LastError = ""
	j.windowPaused = false
	if j.Status == StatusError || j.Status == StatusPaused {
		j.Status = StatusQueued
	}
	m.persistLocked()
	m.nudge()
	return nil
}

// Window returns the current allowed-time window.
func (m *Manager) Window() TimeWindow {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.window
}

// List returns an immutable snapshot of all jobs, newest-added last.
func (m *Manager) List() []JobView {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]JobView, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j.snapshotLocked())
	}
	return out
}

// Get returns a snapshot of one job.
func (m *Manager) Get(id string) (JobView, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.byID[id]
	if !ok {
		return JobView{}, false
	}
	return j.snapshotLocked(), true
}

// Shutdown stops the scheduler and cancels running downloads, keeping their
// resume state so they can continue on next start. It waits for goroutines to
// exit and persists the final list.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	close(m.stop)
	for _, j := range m.jobs {
		if j.Status == StatusRunning && j.cancel != nil {
			j.cancel()
		}
	}
	m.mu.Unlock()

	m.wg.Wait()

	m.mu.Lock()
	// Any job still marked running was cancelled mid-flight: mark it paused.
	for _, j := range m.jobs {
		if j.Status == StatusRunning {
			j.Status = StatusPaused
		}
	}
	m.persistLocked()
	m.mu.Unlock()
}

// --- internal ---

// scheduler is the background loop that starts queued jobs up to the limit. It
// wakes on explicit nudges and on a periodic tick so time-based transitions
// (StartAt reached, allowed window opening/closing) happen without an event.
func (m *Manager) scheduler() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-m.wake:
			m.reconcile()
		case <-ticker.C:
			m.reconcile()
		}
	}
}

// reconcile brings the queue in line with the current time: promotes scheduled
// jobs whose StartAt has passed, pauses running jobs if the allowed window has
// closed, and starts eligible jobs up to the concurrency limit.
func (m *Manager) reconcile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	now := m.now()
	windowOpen := m.window.contains(now)

	// If the window is closed, pause anything running and start nothing. Mark
	// those pauses as window-caused so they auto-resume when the window reopens.
	if !windowOpen {
		for _, j := range m.jobs {
			if j.Status == StatusRunning && j.cancel != nil {
				j.windowPaused = true
				j.cancel() // runJob will mark it paused and keep resume state
			}
		}
		return
	}

	// Window is open. Auto-resume anything the window previously paused.
	for _, j := range m.jobs {
		if j.windowPaused && j.Status == StatusPaused {
			j.windowPaused = false
			j.Status = StatusQueued
		}
	}

	// Promote scheduled jobs whose time has come.
	for _, j := range m.jobs {
		if j.Status == StatusScheduled && (j.StartAt.IsZero() || !j.StartAt.After(now)) {
			j.Status = StatusQueued
		}
	}

	// Start queued jobs up to the limit.
	for _, j := range m.nextQueuedLocked() {
		if m.running >= m.maxConcurrent {
			break
		}
		m.startJobLocked(j)
	}
}

// nextQueuedLocked returns queued jobs in add order. Caller holds the lock.
func (m *Manager) nextQueuedLocked() []*Job {
	var q []*Job
	for _, j := range m.jobs {
		if j.Status == StatusQueued {
			q = append(q, j)
		}
	}
	sort.SliceStable(q, func(a, b int) bool { return q[a].AddedAt.Before(q[b].AddedAt) })
	return q
}

// startJobLocked transitions a job to running and spawns its download goroutine.
// Caller holds the lock.
func (m *Manager) startJobLocked(j *Job) {
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.Status = StatusRunning
	j.LastError = ""
	m.running++
	m.persistLocked()

	m.wg.Add(1)
	go m.runJob(ctx, j)
}

// runJob executes a single download and updates the job on completion.
func (m *Manager) runJob(ctx context.Context, j *Job) {
	defer m.wg.Done()

	opts := j.toEngineOptions(0)
	opts.Limiter = m.limiter
	opts.ConnLimit = m.connLimitFor(j.URL)
	d := engine.New(opts)
	err := d.Download(ctx, func(p engine.Progress) {
		m.mu.Lock()
		j.TotalSize = p.TotalSize
		j.Downloaded = p.Downloaded
		j.speedBps = p.SpeedBps
		m.mu.Unlock()
	})

	m.mu.Lock()
	m.running--
	j.cancel = nil
	j.speedBps = 0
	switch {
	case err == nil:
		j.Status = StatusDone
		j.CompletedAt = time.Now()
	case ctx.Err() != nil:
		// Cancelled by Pause/Remove/Shutdown. If the job still exists, mark paused.
		if _, ok := m.byID[j.ID]; ok {
			j.Status = StatusPaused
		}
	default:
		j.Status = StatusError
		j.LastError = err.Error()
	}
	m.persistLocked()
	m.mu.Unlock()

	// A slot freed up; let the scheduler start the next job.
	m.nudge()
}

// nudge signals the scheduler without blocking if a signal is already pending.
func (m *Manager) nudge() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// load reads a persisted job list, if present. Running jobs become paused so the
// user decides whether to resume after a restart.
func (m *Manager) load() error {
	b, err := os.ReadFile(m.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var ps persistState
	if err := json.Unmarshal(b, &ps); err != nil {
		// Corrupt state should not prevent startup; begin with an empty list.
		return nil
	}
	if ps.MaxConcurrent > 0 {
		m.maxConcurrent = ps.MaxConcurrent
	}
	if ps.MaxBytesPerSec > 0 {
		m.maxBytesPerSec = ps.MaxBytesPerSec
	}
	m.window = ps.Window
	m.downloadFolder = ps.DownloadFolder
	for _, j := range ps.Jobs {
		if j.Status == StatusRunning {
			j.Status = StatusQueued // nothing is actually running yet; let it restart
		}
		j.cancel = nil
		j.speedBps = 0
		j.windowPaused = false
		m.jobs = append(m.jobs, j)
		m.byID[j.ID] = j
	}
	return nil
}

// persistLocked writes the job list atomically. Caller holds the lock.
func (m *Manager) persistLocked() {
	ps := persistState{Jobs: m.jobs, MaxConcurrent: m.maxConcurrent, MaxBytesPerSec: m.maxBytesPerSec, Window: m.window, DownloadFolder: m.downloadFolder}
	b, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return
	}
	if dir := filepath.Dir(m.statePath); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	tmp := m.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, m.statePath)
}

// newID returns a short random hex identifier for a job.
func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
