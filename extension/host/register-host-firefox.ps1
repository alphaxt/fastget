# register-host-firefox.ps1
#
# Registers the fastget native-messaging host with Firefox on Windows.
#
# Firefox differs from Chrome/Edge in two ways:
#   1. The host manifest uses "allowed_extensions" (the add-on's gecko ID) rather
#      than Chrome's "allowed_origins".
#   2. It is registered under HKCU\Software\Mozilla\NativeMessagingHosts\<name>.
#
# The add-on ID is fixed in the extension manifest
# (browser_specific_settings.gecko.id), so you do not need to look it up the way
# you do for Chrome. The default matches extension/manifest.json.
#
# Usage:
#   .\register-host-firefox.ps1
#   .\register-host-firefox.ps1 -ExtensionId fastget@fastget.local
#   .\register-host-firefox.ps1 -Unregister

param(
    [string]$ExtensionId = "fastget@fastget.addon",
    [switch]$Unregister
)

$ErrorActionPreference = "Stop"
$HostName = "com.fastget.host"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot  = Resolve-Path (Join-Path $scriptDir "..\..")   # ...\downloader
$hostExe   = Join-Path $repoRoot "fastget-host.exe"
# Keep the Firefox manifest separate from the Chrome one so both can coexist.
$manifestOut = Join-Path $scriptDir "$HostName.firefox.json"

# Firefox's per-user native-messaging registry location.
$firefoxKey = "HKCU:\Software\Mozilla\NativeMessagingHosts\$HostName"

if ($Unregister) {
    if (Test-Path $firefoxKey) { Remove-Item $firefoxKey -Force; Write-Host "removed $firefoxKey" }
    Write-Host "Unregistered $HostName for Firefox."
    return
}

if (-not (Test-Path $hostExe)) {
    Write-Error "Host binary not found at $hostExe. Build it first: go build -o fastget-host.exe ./cmd/fastget-host"
}

# Write a concrete manifest with the real host path and the add-on ID.
$escapedPath = $hostExe -replace '\\', '\\'
$manifest = @"
{
  "name": "$HostName",
  "description": "fastget native messaging host",
  "path": "$escapedPath",
  "type": "stdio",
  "allowed_extensions": [
    "$ExtensionId"
  ]
}
"@
Set-Content -Path $manifestOut -Value $manifest -Encoding UTF8
Write-Host "wrote manifest: $manifestOut"

# Point Firefox at the manifest via the key's default value.
New-Item -Path $firefoxKey -Force | Out-Null
Set-ItemProperty -Path $firefoxKey -Name "(default)" -Value $manifestOut
Write-Host "registered: $firefoxKey"

Write-Host ""
Write-Host "Done. In Firefox, load the extension via about:debugging > This Firefox >"
Write-Host "Load Temporary Add-on, pick extension/manifest.json, then test a download."
