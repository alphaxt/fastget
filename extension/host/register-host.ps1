# register-host.ps1
#
# Registers the fastget native-messaging host with Chrome and Edge on Windows.
#
# On Windows, browsers find a native-messaging host by reading a registry key that
# points at the host's manifest JSON. This script:
#   1. Writes a concrete manifest (with the real host path + your extension ID).
#   2. Creates the registry keys for Chrome and Edge pointing at that manifest.
#
# Usage:
#   .\register-host.ps1 -ExtensionId <your-unpacked-extension-id>
#
# Find your extension ID at chrome://extensions (enable Developer mode, load the
# unpacked "extension" folder, copy the ID shown on the card).
#
# Re-run any time the extension ID or host path changes. Use -Unregister to remove.

param(
    [string]$ExtensionId,
    [switch]$Unregister
)

$ErrorActionPreference = "Stop"
$HostName = "com.fastget.host"

# Resolve paths relative to this script's location.
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot  = Resolve-Path (Join-Path $scriptDir "..\..")   # ...\downloader
$hostExe   = Join-Path $repoRoot "fastget-host.exe"
$manifestOut = Join-Path $scriptDir "$HostName.json"

# Registry locations the browsers read (per-user, no admin needed).
$chromeKey = "HKCU:\Software\Google\Chrome\NativeMessagingHosts\$HostName"
$edgeKey   = "HKCU:\Software\Microsoft\Edge\NativeMessagingHosts\$HostName"

if ($Unregister) {
    foreach ($k in @($chromeKey, $edgeKey)) {
        if (Test-Path $k) { Remove-Item $k -Force; Write-Host "removed $k" }
    }
    Write-Host "Unregistered $HostName."
    return
}

if (-not $ExtensionId) {
    Write-Error "Pass -ExtensionId <id>. Get it from chrome://extensions after loading the unpacked extension."
}

if (-not (Test-Path $hostExe)) {
    Write-Error "Host binary not found at $hostExe. Build it first: go build -o fastget-host.exe ./cmd/fastget-host"
}

# Write a concrete manifest with real values. JSON needs backslashes escaped.
$escapedPath = $hostExe -replace '\\', '\\'
$manifest = @"
{
  "name": "$HostName",
  "description": "fastget native messaging host",
  "path": "$escapedPath",
  "type": "stdio",
  "allowed_origins": [
    "chrome-extension://$ExtensionId/"
  ]
}
"@
Set-Content -Path $manifestOut -Value $manifest -Encoding UTF8
Write-Host "wrote manifest: $manifestOut"

# Point Chrome and Edge at the manifest via their default-value registry keys.
foreach ($k in @($chromeKey, $edgeKey)) {
    New-Item -Path $k -Force | Out-Null
    Set-ItemProperty -Path $k -Name "(default)" -Value $manifestOut
    Write-Host "registered: $k"
}

Write-Host ""
Write-Host "Done. Restart the browser, then start a download to test the catch."
