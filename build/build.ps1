# build.ps1 — compile the three fastget binaries into build\dist.
#
# Produces: dist\fastget.exe, dist\fastget-host.exe, dist\fastget-ui.exe
# plus a copy of the browser extension, ready to be packaged by an installer.
#
# Usage:
#   .\build\build.ps1
#   .\build\build.ps1 -Go "C:\Program Files\Go\bin\go.exe"   # explicit Go path

param(
    [string]$Go = "go"
)

$ErrorActionPreference = "Stop"

# Repo root is the parent of this script's folder.
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$root = Resolve-Path (Join-Path $scriptDir "..")
$dist = Join-Path $scriptDir "dist"

# Resolve Go: prefer -Go, else PATH, else the common Windows install location.
if (-not (Get-Command $Go -ErrorAction SilentlyContinue)) {
    $fallback = "C:\Program Files\Go\bin\go.exe"
    if (Test-Path $fallback) { $Go = $fallback }
    else { throw "Go not found. Install from https://go.dev/dl/ or pass -Go <path>." }
}

Write-Host "using Go: $Go"
Write-Host "repo root: $root"

# Fresh dist.
if (Test-Path $dist) { Remove-Item -Recurse -Force $dist }
New-Item -ItemType Directory -Path $dist | Out-Null

# Build each command. CGO is not needed by any of them.
$env:CGO_ENABLED = "0"
Push-Location $root
try {
    foreach ($cmd in @("fastget", "fastget-host", "fastget-ui")) {
        $out = Join-Path $dist "$cmd.exe"
        Write-Host "building $cmd -> $out"
        if ($cmd -eq "fastget-ui") {
            # Windowed program: no console window behind the tray app.
            & $Go build -ldflags "-H windowsgui" -o $out "./cmd/$cmd"
        } else {
            & $Go build -o $out "./cmd/$cmd"
        }
        if ($LASTEXITCODE -ne 0) { throw "build failed for $cmd" }
    }
}
finally {
    Pop-Location
}

# Bundle the browser extension alongside the binaries, but only the files the
# browser actually loads. The host/ folder (PowerShell scripts, manifests) and
# the docs are NOT part of the WebExtension — the installer writes the host
# manifests itself, so users never run a script.
$extSrc = Join-Path $root "extension"
$extDst = Join-Path $dist "extension"
New-Item -ItemType Directory -Path $extDst | Out-Null
$webFiles = @(
    "manifest.json", "background.js",
    "popup.html", "popup.js",
    "dialog.html", "dialog.js",
    "setup.html", "setup.js",
    "privacy.html"
)
foreach ($f in $webFiles) { Copy-Item (Join-Path $extSrc $f) $extDst }
Copy-Item -Recurse (Join-Path $extSrc "icons") (Join-Path $extDst "icons")

# Also produce the exact zip to upload to addons.mozilla.org for signing.
# NOTE: We use ZipArchive directly (not Compress-Archive) so that entry names
# use forward-slash separators (icons/icon48.png) instead of Windows backslashes.
# AMO's validator rejects backslash paths in the archive.
$zip = Join-Path $scriptDir "fastget-extension.zip"
if (Test-Path $zip) { Remove-Item $zip }

Add-Type -AssemblyName System.IO.Compression
$zipStream = [System.IO.File]::Open($zip, [System.IO.FileMode]::Create)
$zipArchive = [System.IO.Compression.ZipArchive]::new($zipStream, [System.IO.Compression.ZipArchiveMode]::Create)
$allExtFiles = Get-ChildItem -Recurse -File $extDst
foreach ($file in $allExtFiles) {
    # Build a forward-slash entry name relative to $extDst
    $entryName = $file.FullName.Substring($extDst.Length + 1).Replace("\", "/")
    $entry = $zipArchive.CreateEntry($entryName, [System.IO.Compression.CompressionLevel]::Optimal)
    $entryStream = $entry.Open()
    $bytes = [System.IO.File]::ReadAllBytes($file.FullName)
    $entryStream.Write($bytes, 0, $bytes.Length)
    $entryStream.Dispose()
}
$zipArchive.Dispose()
$zipStream.Dispose()

Write-Host ""
Write-Host "done."
Write-Host "dist (for the installer):"
Get-ChildItem $dist | Select-Object Name, Length | Format-Table -AutoSize
Write-Host "AMO upload package: $zip"
