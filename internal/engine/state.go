package engine

import (
	"encoding/json"
	"os"
	"sync/atomic"
)

// stateSuffix is appended to the output path to form the resume sidecar file.
const stateSuffix = ".fgdl"

// statePath returns the sidecar path for a given output file.
func statePath(outputPath string) string {
	return outputPath + stateSuffix
}

// loadState reads a persisted resume state, if one exists. A missing file is not
// an error: it simply means there is nothing to resume.
func loadState(outputPath string) (*State, error) {
	b, err := os.ReadFile(statePath(outputPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		// A corrupt sidecar should not wedge the user; treat it as no state.
		return nil, nil
	}
	return &st, nil
}

// saveState writes the resume state atomically (write-temp-then-rename) so a crash
// mid-write cannot leave a half-written, unparseable sidecar.
func saveState(st *State) error {
	// Copy the progress counters atomically: connections are still updating
	// them while this runs.
	// Runs of finished pieces are merged into one entry, so the file stays a
	// few hundred bytes instead of thousands of entries. rechunk() splits the
	// unfinished parts again on resume.
	snap := *st
	snap.Segments = make([]Segment, 0, 16)
	for i := range st.Segments {
		s := st.Segments[i]
		s.Downloaded = atomic.LoadInt64(&st.Segments[i].Downloaded)
		if n := len(snap.Segments); n > 0 && s.complete() {
			prev := &snap.Segments[n-1]
			if prev.complete() && prev.End+1 == s.Start {
				prev.End = s.End
				prev.Downloaded += s.Downloaded
				continue
			}
		}
		snap.Segments = append(snap.Segments, s)
	}
	b, err := json.Marshal(&snap)
	if err != nil {
		return err
	}
	tmp := statePath(st.OutputPath) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(st.OutputPath))
}

// clearState removes the resume sidecar once a download has completed successfully.
func clearState(outputPath string) {
	_ = os.Remove(statePath(outputPath))
}

// canResume reports whether a previously saved state matches the current remote
// file well enough to continue. If the validators differ, the remote file likely
// changed and we must restart from scratch.
func (st *State) canResume(url string, totalSize int64, etag, lastMod string) bool {
	if st == nil || !st.Resumable {
		return false
	}
	if st.URL != url || st.TotalSize != totalSize {
		return false
	}
	// Prefer the strong validator (ETag). Fall back to Last-Modified only when
	// neither side advertised an ETag.
	if st.ETag != "" || etag != "" {
		return st.ETag == etag
	}
	return st.LastModified == lastMod
}
