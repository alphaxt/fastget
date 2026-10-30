# autostart.ps1 — enable or disable launching fastget on Windows login.
#
# Adds/removes a per-user HKCU "Run" entry that starts the desktop app minimized
# to the system tray when you sign in. No admin required.
#
# Usage:
#   .\autostart.ps1 -Enable                      # uses the installed location
#   .\autostart.ps1 -Enable -ExePath C:\path\fastget-ui.exe
#   .\autostart.ps1 -Disable
#   .\autostart.ps1 -Status

param(
    [switch]$Enable,
    [switch]$Disable,
    [switch]$Status,
    [string]$ExePath
)

$ErrorActionPreference = "Stop"
$AppName = "fastget"
$runKey  = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"

function Get-Current {
    (Get-ItemProperty -Path $runKey -Name $AppName -ErrorAction SilentlyContinue).$AppName
}

if ($Status -or (-not $Enable -and -not $Disable)) {
    $cur = Get-Current
    if ($cur) { Write-Host "auto-start: ENABLED -> $cur" }
    else { Write-Host "auto-start: disabled" }
    return
}

if ($Disable) {
    if (Get-ItemProperty -Path $runKey -Name $AppName -ErrorAction SilentlyContinue) {
        Remove-ItemProperty -Path $runKey -Name $AppName
        Write-Host "auto-start disabled."
    } else {
        Write-Host "auto-start was not enabled."
    }
    return
}

if ($Enable) {
    if (-not $ExePath) {
        # Default to the standard per-user install location.
        $ExePath = Join-Path $env:LOCALAPPDATA "fastget\fastget-ui.exe"
    }
    if (-not (Test-Path $ExePath)) {
        throw "fastget-ui.exe not found at '$ExePath'. Install first, or pass -ExePath."
    }
    $cmd = '"{0}" --tray' -f $ExePath
    Set-ItemProperty -Path $runKey -Name $AppName -Value $cmd
    Write-Host "auto-start enabled: $cmd"
}
