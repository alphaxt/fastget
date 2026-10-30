; fastget.iss — Inno Setup script for a polished Windows installer.
;
; Compile with Inno Setup (free: https://jrsoftware.org/isinfo.php):
;   1. Run build\build.ps1 first so build\dist contains the binaries + extension.
;   2. Open this file in the Inno Setup Compiler and click Compile, or run:
;        iscc build\fastget.iss
;   The result is build\Output\fastget-setup.exe.
;
; This is a per-user install (no admin required). It installs the three binaries
; and the extension, creates Start Menu and optional desktop shortcuts, and offers
; an auto-start-on-login task.

#define AppName "fastget"
#define AppVersion "0.1.0"
#define AppPublisher "fastget"
#define AppExeName "fastget-ui.exe"

[Setup]
AppId={{B6B8C2F0-2B4A-4B2E-9E3C-FASTGET00001}}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
; Per-user install: no admin needed.
PrivilegesRequired=lowest
DefaultDirName={userappdata}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
OutputDir=Output
OutputBaseFilename=fastget-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a &desktop shortcut"; GroupDescription: "Additional shortcuts:"; Flags: unchecked
Name: "autostart"; Description: "Start fastget automatically when I sign in (minimized to the system tray)"; GroupDescription: "Startup:"; Flags: unchecked

[Files]
; Built binaries (produced by build\build.ps1 into build\dist).
Source: "dist\fastget.exe";      DestDir: "{app}"; Flags: ignoreversion
Source: "dist\fastget-host.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "dist\fastget-ui.exe";   DestDir: "{app}"; Flags: ignoreversion
; The browser extension and its host-registration scripts.
Source: "dist\extension\*"; DestDir: "{app}\extension"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\fastget";          Filename: "{app}\{#AppExeName}"
Name: "{group}\Uninstall fastget"; Filename: "{uninstallexe}"
Name: "{userdesktop}\fastget";    Filename: "{app}\{#AppExeName}"; Tasks: desktopicon

[Registry]
; Optional auto-start on login (launches minimized to tray).
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; \
    ValueType: string; ValueName: "fastget"; \
    ValueData: """{app}\{#AppExeName}"" --tray"; \
    Flags: uninsdeletevalue; Tasks: autostart

[Run]
; Offer to launch the app at the end of setup.
Filename: "{app}\{#AppExeName}"; Description: "Launch fastget"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; Remove the per-user queue/config state on uninstall.
Type: filesandordirs; Name: "{userappdata}\fastget"
