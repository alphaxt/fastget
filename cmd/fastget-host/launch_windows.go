//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

const (
	createBreakawayFromJob = 0x01000000
	createNewProcessGroup  = 0x00000200
	detachedProcess        = 0x00000008
)

// startDetached launches the desktop app so it outlives this host process.
//
// Firefox runs native hosts inside a Windows job object that is torn down when
// the host exits. CREATE_BREAKAWAY_FROM_JOB moves the app out of that job so it
// keeps running (and keeps downloading) after the browser disconnects. If the
// job does not allow breakaway, we retry without it.
func startDetached(exe string, args ...string) error {
	try := func(flags uint32) error {
		cmd := exec.Command(exe, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	if err := try(createBreakawayFromJob | createNewProcessGroup | detachedProcess); err == nil {
		return nil
	}
	return try(createNewProcessGroup | detachedProcess)
}
