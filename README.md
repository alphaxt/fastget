# fastget

A clean-room, multi-connection download engine written in Go. It provides the
capabilities that make download accelerators feel fast, built entirely from
standard, public HTTP techniques:

- **Multi-connection downloading** — a file is split into N byte-range segments
  fetched in parallel via the HTTP `Range` header.
- **Pause / resume** — per-segment progress is persisted to a sidecar `.fgdl`
  file, so an interrupted download continues instead of restarting.
- **Retry with backoff** — a transient failure on one segment is retried without
  failing the whole download.
- **Live speed reporting** — aggregate throughput across all connections.
- **Graceful fallback** — servers that don't support ranges get a correct
  single-stream download.

This is an independent implementation. It is not derived from, and does not copy,
any proprietary download manager. These are the standard mechanisms parallel
HTTP downloading is built on.

## Requirements

- Go 1.22+ (install from https://go.dev/dl/)

## Build

```powershell
cd e:\reverse\downloader
go build -o fastget.exe ./cmd/fastget
```

## Run

```powershell
# 16 connections, explicit output name
.\fastget.exe -c 16 -o ubuntu.iso https://example.com/big/ubuntu.iso

# default 8 connections, filename derived from the URL
.\fastget.exe https://example.com/big/file.zip
```

Press Ctrl+C to interrupt. Re-run the exact same command to resume where it
stopped (the engine validates the remote file via ETag/Last-Modified before
resuming, and restarts cleanly if the remote file changed).

## Flags

| Flag        | Default | Meaning                                         |
|-------------|---------|-------------------------------------------------|
| `-c`        | 8       | Number of parallel connections (segments)       |
| `-o`        | (auto)  | Output file path; derived from the URL if unset |
| `-retries`  | 5       | Per-segment retry attempts on transient errors  |
| `-timeout`  | 30s     | Per-request timeout (`0` disables it)           |
| `-ua`       | (auto)  | Custom `User-Agent` header                      |

## Desktop app (download list UI)

A local web UI over the engine and queue — the "download list" experience, with
live progress, pause/resume/remove, and a parallel-downloads setting.

```powershell
go build -o fastget-ui.exe ./cmd/fastget-ui
.\fastget-ui.exe
```

It binds to `127.0.0.1:7799` and opens your browser. Flags:

| Flag         | Default            | Meaning                                  |
|--------------|--------------------|------------------------------------------|
| `-addr`      | `127.0.0.1:7799`   | Address to bind (keep it on loopback)    |
| `-parallel`  | 3                  | How many downloads run at once           |
| `-state`     | user config dir    | Where the queue list is persisted        |
| `-no-open`   | false              | Don't auto-open the browser              |

The queue persists across restarts; anything running when you quit is paused and
resumable next launch.

Scheduling: give a download a start time with the "start at" field, or set an
allowed time-of-day window (e.g. only download 00:00–06:00). Outside the window,
running downloads pause and resume automatically when it reopens.

### Run in the background (system tray)

```powershell
.\fastget-ui.exe --tray
```

In `--tray` mode the app runs quietly with a Windows notification-area icon
(right-click for **Open fastget** / **Quit**) instead of opening a browser on
start. This is what the auto-start entry uses. The tray is pure Go (Win32 via the
standard library, no cgo); on non-Windows platforms `--tray` just runs headless.

### Start on login

```powershell
# enable (points at the installed fastget-ui.exe by default)
.\build\autostart.ps1 -Enable
.\build\autostart.ps1 -Status
.\build\autostart.ps1 -Disable
```

The installer can also set this up for you (see `build/README.md`).

## Browser integration

Catch downloads directly from Chrome/Edge and hand them to the engine. See
`extension/README.md` for install steps (load the unpacked `extension` folder,
then run `extension/host/register-host.ps1`).

## Install / package

See `build/README.md`: `build.ps1` compiles all three binaries, and either
`install.ps1` (no dependencies) or `fastget.iss` (Inno Setup) packages them.

## Project layout

```
downloader/
  cmd/
    fastget/        CLI entrypoint + progress bar
    fastget-host/   native-messaging host for the browser extension
    fastget-ui/     local web UI (download list)
  internal/
    engine/         the engine: probe, plan, parallel fetch, stitch, resume
    queue/          job list + scheduler (concurrency limit, pause/resume, persist)
    ui/             HTTP server + embedded static UI
  extension/        the WebExtension + native-host registration
  tools/makeicons/  generates the extension icons
```

## Roadmap (remaining ideas)

- **Speed limiter** — token-bucket rate control.
- **Integrity checks** — verify size and optional hashes after stitching.
- **Firefox host registration** — script the Firefox-specific manifest/registry.
