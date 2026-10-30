package queue

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeClock is a controllable time source for deterministic scheduling tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// newManagerWithClock builds a manager and swaps in a fake clock before the
// scheduler does meaningful work.
func newManagerWithClock(t *testing.T, statePath string, maxConc int, clk *fakeClock) *Manager {
	t.Helper()
	m, err := NewManager(statePath, maxConc)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.now = clk.now
	m.mu.Unlock()
	return m
}

// TestStartAtSchedulesThenRuns verifies a future StartAt keeps the job in
// StatusScheduled until the clock passes it, then it runs to completion.
func TestStartAtSchedulesThenRuns(t *testing.T) {
	srv := rangeServer2(t, 512*1024)
	defer srv.Close()

	clk := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)}
	dir := t.TempDir()
	m := newManagerWithClock(t, filepath.Join(dir, "q.json"), 2, clk)
	defer m.Shutdown()

	out := filepath.Join(dir, "later.bin")
	id, err := m.Add(AddOptions{
		URL:         srv.URL,
		OutputPath:  out,
		Connections: 4,
		StartAt:     clk.now().Add(1 * time.Hour), // one hour in the "future"
	})
	if err != nil {
		t.Fatal(err)
	}

	// It should sit scheduled, not run.
	time.Sleep(1200 * time.Millisecond) // let a scheduler tick happen
	if v, _ := m.Get(id); v.Status != StatusScheduled {
		t.Fatalf("expected scheduled, got %s", v.Status)
	}

	// Advance past StartAt; the next tick should promote and run it.
	clk.advance(2 * time.Hour)
	if !waitFor(t, 20*time.Second, func() bool {
		v, _ := m.Get(id)
		return v.Status == StatusDone
	}) {
		v, _ := m.Get(id)
		t.Fatalf("job did not run after StartAt passed; status=%s", v.Status)
	}
}

// TestWindowBlocksAndReleases verifies that a closed allowed-window prevents a
// job from starting, and opening the window lets it run.
func TestWindowBlocksAndReleases(t *testing.T) {
	srv := rangeServer2(t, 512*1024)
	defer srv.Close()

	// Clock at 12:00. Window allows only 00:00-06:00, so 12:00 is closed.
	clk := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)}
	dir := t.TempDir()
	m := newManagerWithClock(t, filepath.Join(dir, "q.json"), 2, clk)
	defer m.Shutdown()

	m.SetWindow(TimeWindow{Enabled: true, Start: 0, End: 6 * 60}) // 00:00-06:00

	out := filepath.Join(dir, "windowed.bin")
	id, _ := m.Add(AddOptions{URL: srv.URL, OutputPath: out, Connections: 4})

	// Window is closed at 12:00: job must not reach done.
	time.Sleep(1200 * time.Millisecond)
	if v, _ := m.Get(id); v.Status == StatusDone {
		t.Fatalf("job ran while window was closed")
	}

	// Move the clock into the window (03:00) and it should run.
	clk.mu.Lock()
	clk.t = time.Date(2026, 1, 1, 3, 0, 0, 0, time.Local)
	clk.mu.Unlock()

	if !waitFor(t, 20*time.Second, func() bool {
		v, _ := m.Get(id)
		return v.Status == StatusDone
	}) {
		v, _ := m.Get(id)
		t.Fatalf("job did not run after window opened; status=%s", v.Status)
	}
}

// TestWindowContains unit-tests the wrap-around window logic directly.
func TestWindowContains(t *testing.T) {
	day := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.Local) }

	// Normal window 09:00-17:00.
	w := TimeWindow{Enabled: true, Start: 9 * 60, End: 17 * 60}
	if !w.contains(day(12, 0)) || w.contains(day(18, 0)) || w.contains(day(8, 0)) {
		t.Fatalf("normal window logic wrong")
	}
	// Wrapping window 22:00-06:00.
	w = TimeWindow{Enabled: true, Start: 22 * 60, End: 6 * 60}
	if !w.contains(day(23, 0)) || !w.contains(day(2, 0)) || w.contains(day(12, 0)) {
		t.Fatalf("wrapping window logic wrong")
	}
	// Disabled window allows everything.
	w = TimeWindow{Enabled: false}
	if !w.contains(day(3, 0)) || !w.contains(day(15, 0)) {
		t.Fatalf("disabled window should always allow")
	}
}
