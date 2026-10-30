# fastget

A fast, clean-room download manager for Windows: a multi-connection download
engine, a desktop download list, and a browser extension that catches downloads
and hands them to the app.

Repository: https://github.com/alphaxt/fastget

It is built entirely from standard, public HTTP techniques and does not copy any
proprietary download manager.

## Features

- **Multi-connection downloading** — each file is split into byte-range pieces
  fetched in parallel (HTTP `Range`) and joined back together.
- **Pause / resume** — per-file progress is persisted to a `.fgdl` sidecar, so an
  interrupted download continues instead of restarting.
- **Per-server connection sharing** — several downloads from one server share a
  fair pool of connections instead of flooding it.
- **Speed limit** — a single cap on the combined speed of all downloads, change
  it live.
- **Scheduling** — start a download at a set time, or only download within an
  allowed time-of-day window.
- **Integrity check** — optional size + SHA-256 verification after a download.
- **Choose where to save** — pick any folder/drive, with a free-space check.
- **Browser catch** — Chrome, Edge and Firefox, with an IDM-style confirm popup
  and a live progress window (speed, time left, pause/resume/cancel).
- **System tray + start on login.**
- **Retry with backoff and a stall watchdog** for flaky connections.

## Install (for users)

Download the Windows installer from the
[latest release](https://github.com/alphaxt/fastget/releases/latest) and run it.
It installs the app and connects the browser extension automatically — no command
line. Then add the extension from the browser store (or load it for development,
see below).

## Components

| Binary            | What it is                                              |
|-------------------|---------------------------------------------------------|
| `fastget.exe`     | command-line downloader                                 |
| `fastget-ui.exe`  | desktop app: download list + per-download progress UI   |
| `fastget-host.exe`| native-messaging host the browser extension talks to    |

## Build from source

Requires Go 1.22+ (https://go.dev/dl/).

```powershell
# all three binaries + the browser extension package + the AMO zip:
.\build\build.ps1
# or, if `go` isn't on PATH:
.\build\build.ps1 -Go "C:\Program Files\Go\bin\go.exe"
```

See `build/README.md` for packaging into an installer, and `extension/README.md`
for loading the extension during development.

## Command-line usage

```powershell
# 16 connections, explicit output name
.\fastget.exe -c 16 -o ubuntu.iso https://example.com/big/ubuntu.iso

# default 8 connections, name taken from the URL
.\fastget.exe https://example.com/big/file.zip
```

| Flag        | Default | Meaning                                         |
|-------------|---------|-------------------------------------------------|
| `-c`        | 8       | Parallel connections                            |
| `-o`        | (auto)  | Output path; derived from the URL if unset      |
| `-limit`    | (none)  | Max speed, e.g. `500k`, `2m`                     |
| `-sha256`   | (none)  | Expected SHA-256; fails if the result differs   |
| `-size`     | 0       | Expected size in bytes                           |
| `-retries`  | 5       | Per-piece retry attempts                        |
| `-ua`       | (auto)  | Custom `User-Agent` header                      |

Press Ctrl+C to interrupt; re-run the same command to resume.

## Desktop app

```powershell
.\fastget-ui.exe            # opens the download list in your browser
.\fastget-ui.exe --tray     # runs in the system tray (used for start-on-login)
```

It serves a local UI on `127.0.0.1:7799`. The queue persists across restarts.
Start-on-login can be toggled with `.\build\autostart.ps1 -Enable` / `-Disable`,
or during install.

## Project layout

```
downloader/
  cmd/
    fastget/        CLI
    fastget-host/   native-messaging host for the browser extension
    fastget-ui/     desktop app: HTTP server + system tray
  internal/
    engine/         download engine: probe, plan, parallel fetch, stitch, resume,
                    rate limit, per-server connection sharing, verify
    queue/          job list + scheduler (concurrency, pause/resume, schedule, persist)
    ui/             HTTP server + embedded download list and progress pages
    fsutil/         file names, free space, disk-full detection
    picker/         native "choose folder" dialog
    tray/           Windows system-tray icon (pure Go)
  extension/        the WebExtension (+ dev-only host registration scripts)
  build/            build + packaging scripts and the Inno Setup installer
  tools/makeicons/  generates the extension icons
```

## License

See `LICENSE`.
