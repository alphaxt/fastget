//go:build windows

package picker

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
)

const createNoWindow = 0x08000000

// script shows the standard Windows folder dialog on top of other windows and
// prints the chosen path as UTF-8. The start folder arrives in an environment
// variable, so a folder name can never be interpreted as PowerShell code.
const script = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.Application]::EnableVisualStyles()
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
$owner.ShowInTaskbar = $false
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = 'Choose where fastget saves the download'
$d.ShowNewFolderButton = $true
$start = $env:FASTGET_PICK_INITIAL
if ($start -and (Test-Path -LiteralPath $start -PathType Container)) { $d.SelectedPath = $start }
$r = $d.ShowDialog($owner)
$owner.Dispose()
if ($r -eq [System.Windows.Forms.DialogResult]::OK -and $d.SelectedPath) {
  $b = [System.Text.Encoding]::UTF8.GetBytes($d.SelectedPath)
  $o = [Console]::OpenStandardOutput()
  $o.Write($b, 0, $b.Length)
  $o.Flush()
}
`

func pickFolder(initial string) (string, error) {
	ps := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(ps); err != nil {
		ps = "powershell.exe"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ps,
		"-NoProfile", "-NonInteractive", "-STA", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encode(script))
	cmd.Env = append(os.Environ(), "FASTGET_PICK_INITIAL="+initial)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return "", &pickError{msg: msg}
		}
		return "", err
	}
	folder := strings.TrimSpace(out.String())
	if folder == "" {
		return "", ErrCancelled
	}
	return folder, nil
}

type pickError struct{ msg string }

func (e *pickError) Error() string { return "folder dialog failed: " + e.msg }

// encode turns a script into PowerShell's -EncodedCommand form (UTF-16LE, base64).
func encode(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}
