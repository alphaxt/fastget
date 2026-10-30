package queue

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMoveChangesFolderAndRequeues: moving a failed download changes its save
// path, takes the partial file and resume data along, and queues it again.
func TestMoveChangesFolderAndRequeues(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(filepath.Join(dir, "q.json"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()

	// Keep the scheduler from starting anything: it is 12:00 and downloads are
	// only allowed 01:00-02:00.
	m.mu.Lock()
	m.now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local) }
	m.window = TimeWindow{Enabled: true, Start: 60, End: 120}
	m.mu.Unlock()

	oldDir := filepath.Join(dir, "C")
	newDir := filepath.Join(dir, "H")
	os.MkdirAll(oldDir, 0o755)
	os.MkdirAll(newDir, 0o755)
	oldPath := filepath.Join(oldDir, "big.rar")
	newPath := filepath.Join(newDir, "big.rar")
	os.WriteFile(oldPath, []byte("partial"), 0o644)
	os.WriteFile(oldPath+".fgdl", []byte("{}"), 0o644)

	// A download that failed because the disk was full.
	m.mu.Lock()
	j := &Job{ID: "j1", URL: "http://127.0.0.1:1/x", OutputPath: oldPath, Status: StatusError, LastError: "disk full"}
	m.jobs = append(m.jobs, j)
	m.byID[j.ID] = j
	m.mu.Unlock()

	if err := m.Move("j1", newPath); err != nil {
		t.Fatal(err)
	}
	v, _ := m.Get("j1")
	if v.OutputPath != newPath {
		t.Fatalf("output path = %s, want %s", v.OutputPath, newPath)
	}
	if v.Status != StatusQueued || v.LastError != "" {
		t.Fatalf("after move: status=%s err=%q, want queued with no error", v.Status, v.LastError)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("partial file was not moved: %v", err)
	}
	if _, err := os.Stat(newPath + ".fgdl"); err != nil {
		t.Fatalf("resume data was not moved: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old partial file is still there")
	}
}

// TestMoveRefusesRunningDownload: a running download must be paused first.
func TestMoveRefusesRunningDownload(t *testing.T) {
	m, err := NewManager(filepath.Join(t.TempDir(), "q.json"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	m.mu.Lock()
	j := &Job{ID: "r1", URL: "http://127.0.0.1:1/x", Status: StatusRunning}
	m.jobs = append(m.jobs, j)
	m.byID[j.ID] = j
	m.mu.Unlock()
	if err := m.Move("r1", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatalf("moving a running download should fail")
	}
}
