# install.ps1 — no-dependency installer for fastget (Windows, per-user).
#
# Installs the three binaries and the extension to a per-user location, adds a
# Start Menu shortcut for the desktop app, and optionally registers the browser
# native-messaging host and a login auto-start entry. Requires no admin rights
# and no external tooling.
#
# Run it from the folder produced by build.ps1 (build\dist), or point -Source at
# that folder:
#   .\install.ps1
#   .\install.ps1 -Source C:\path\to\dist
#   .\install.ps1 -Uninstall

param(
    [string]$Source,
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "fastget"),
    [switch]$Uninstall,
    [switch]$AutoStart
)

$ErrorActionPreference = "Stop"
$AppName = "fastget"
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

$startMenu = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"
$shortcut  = Join-Path $startMenu "fastget.lnk"
$runKey    = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"

if ($Uninstall) {
    Write-Host "Uninstalling fastget..."
    # Remove auto-start, shortcut, host registration, and install dir.
    if (Get-ItemProperty -Path $runKey -Name $AppName -ErrorAction SilentlyContinue) {
        Remove-ItemProperty -Path $runKey -Name $AppName
        Write-Host "  removed auto-start entry"
    }
    if (Test-Path $shortcut) { Remove-Item $shortcut; Write-Host "  removed Start Menu shortcut" }
    foreach ($k in @(
        "HKCU:\Software\Google\Chrome\NativeMessagingHosts\com.fastget.host",
        "HKCU:\Software\Microsoft\Edge\NativeMessagingHosts\com.fastget.host",
        "HKCU:\Software\Mozilla\NativeMessagingHosts\com.fastget.host"
    )) {
        if (Test-Path $k) { Remove-Item $k -Force; Write-Host "  removed host registration: $k" }
    }
    if (Test-Path $InstallDir) { Remove-Item -Recurse -Force $InstallDir; Write-Host "  removed $InstallDir" }
    Write-Host "Done. (The browser extension must be removed from the browser manually.)"
    return
}

# Resolve the source folder containing the built binaries.
if (-not $Source) {
    # Default: a "dist" folder next to this script, else the script folder itself.
    $candidate = Join-Path $scriptDir "dist"
    $Source = if (Test-Path (Join-Path $candidate "fastget-ui.exe")) { $candidate } else { $scriptDir }
}
$uiExe = Join-Path $Source "fastget-ui.exe"
if (-not (Test-Path $uiExe)) {
    throw "Could not find fastget-ui.exe in '$Source'. Run build\build.ps1 first, or pass -Source <dist folder>."
}

Write-Host "Installing fastget from: $Source"
Write-Host "            install dir: $InstallDir"

# Copy binaries + extension.
New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
foreach ($f in @("fastget.exe", "fastget-host.exe", "fastget-ui.exe")) {
    $src = Join-Path $Source $f
    if (Test-Path $src) { Copy-Item $src (Join-Path $InstallDir $f) -Force; Write-Host "  installed $f" }
}
$extSrc = Join-Path $Source "extension"
if (Test-Path $extSrc) {
    Copy-Item -Recurse -Force $extSrc (Join-Path $InstallDir "extension")
    Write-Host "  installed extension\"
}

# Start Menu shortcut for the desktop UI.
$ws = New-Object -ComObject WScript.Shell
$lnk = $ws.CreateShortcut($shortcut)
$lnk.TargetPath = Join-Path $InstallDir "fastget-ui.exe"
$lnk.WorkingDirectory = $InstallDir
$lnk.Description = "fastget download manager"
$lnk.Save()
Write-Host "  created Start Menu shortcut"

# Optional login auto-start (launch the UI minimized to tray on sign-in).
if ($AutoStart) {
    $cmd = '"{0}" --tray' -f (Join-Path $InstallDir "fastget-ui.exe")
    Set-ItemProperty -Path $runKey -Name $AppName -Value $cmd
    Write-Host "  enabled auto-start on login"
}

Write-Host ""
Write-Host "Installed. Next steps:"
Write-Host "  - Launch 'fastget' from the Start Menu to open the download list."
Write-Host "  - To catch downloads from the browser, load the extension in"
Write-Host "    '$InstallDir\extension' and run its register-host script"
Write-Host "    (extension\host\register-host.ps1 for Chrome/Edge,"
Write-Host "     register-host-firefox.ps1 for Firefox)."
Write-Host ""
Write-Host "Uninstall any time with:  .\install.ps1 -Uninstall"
