# Packaging fastget

Two ways to package the app for Windows. Both install per-user (no admin).

## 1. Build the binaries

```powershell
cd e:\reverse\downloader
.\build\build.ps1
# or, if `go` isn't on PATH:
.\build\build.ps1 -Go "C:\Program Files\Go\bin\go.exe"
```

This compiles `fastget.exe`, `fastget-host.exe`, and `fastget-ui.exe` into
`build\dist`, and copies the browser `extension` alongside them.

## 2a. Install with the no-dependency PowerShell installer

No extra tools needed.

```powershell
# from build\dist, or point -Source at it:
.\build\install.ps1 -Source .\build\dist

# optionally enable auto-start on login (minimized to tray):
.\build\install.ps1 -Source .\build\dist -AutoStart
```

It installs to `%LOCALAPPDATA%\fastget`, adds a Start Menu shortcut, and prints
the steps to enable browser catching.

Uninstall:

```powershell
.\build\install.ps1 -Uninstall
```

## 2b. Build a setup.exe with Inno Setup (optional, nicer)

Install Inno Setup (free): https://jrsoftware.org/isinfo.php

```powershell
# after build\build.ps1 has populated build\dist:
iscc build\fastget.iss
# produces build\Output\fastget-setup.exe
```

The generated installer offers a desktop shortcut and an auto-start task, and
cleans up the per-user queue state on uninstall.

## After installing

1. Launch **fastget** from the Start Menu to open the download list.
2. To catch browser downloads, load the extension from
   `%LOCALAPPDATA%\fastget\extension` and run the matching register script
   (`extension\host\register-host.ps1` for Chrome/Edge,
   `register-host-firefox.ps1` for Firefox). See `extension\README.md`.
