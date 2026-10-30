; fastget.iss — Windows installer for the fastget app + browser integration.
;
; Build the binaries first (build\build.ps1 puts them in build\dist), then:
;   iscc build\fastget.iss     (Inno Setup: https://jrsoftware.org/isinfo.php)
; The result is build\Output\fastget-setup.exe.
;
; Per-user install (no admin). It installs the three binaries and the extension,
; and — the important part for a public release — it registers the native
; messaging host for Firefox, Chrome and Edge automatically, so the user never
; runs a PowerShell script. It can also add a Start Menu shortcut and start on
; login.

#define AppName "fastget"
#define AppVersion "0.5.0"
#define AppPublisher "fastget"
#define AppExeName "fastget-ui.exe"
#define HostName "com.fastget.host"
#define FirefoxAddonId "fastget@fastget.addon"

[Setup]
AppId={{B6B8C2F0-2B4A-4B2E-9E3C-FASTGET00001}}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
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
Name: "autostart"; Description: "Start fastget automatically when I sign in (in the system tray)"; GroupDescription: "Startup:"; Flags: unchecked

[Files]
Source: "dist\fastget.exe";      DestDir: "{app}"; Flags: ignoreversion
Source: "dist\fastget-host.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "dist\fastget-ui.exe";   DestDir: "{app}"; Flags: ignoreversion
Source: "dist\extension\*"; DestDir: "{app}\extension"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\fastget";           Filename: "{app}\{#AppExeName}"
Name: "{group}\Uninstall fastget"; Filename: "{uninstallexe}"
Name: "{userdesktop}\fastget";     Filename: "{app}\{#AppExeName}"; Tasks: desktopicon

[Registry]
; Optional auto-start on login (in the tray).
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; \
    ValueType: string; ValueName: "fastget"; \
    ValueData: """{app}\{#AppExeName}"" --tray"; \
    Flags: uninsdeletevalue; Tasks: autostart

; Native-messaging host registration. Each browser reads the host's manifest
; from the registry key below; the manifest files are written in [Code].
Root: HKCU; Subkey: "Software\Mozilla\NativeMessagingHosts\{#HostName}"; \
    ValueType: string; ValueName: ""; \
    ValueData: "{app}\host\{#HostName}.firefox.json"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Google\Chrome\NativeMessagingHosts\{#HostName}"; \
    ValueType: string; ValueName: ""; \
    ValueData: "{app}\host\{#HostName}.chrome.json"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Microsoft\Edge\NativeMessagingHosts\{#HostName}"; \
    ValueType: string; ValueName: ""; \
    ValueData: "{app}\host\{#HostName}.chrome.json"; Flags: uninsdeletekey

[Run]
Filename: "{app}\{#AppExeName}"; Description: "Launch fastget"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
Type: filesandordirs; Name: "{app}\host"
Type: filesandordirs; Name: "{userappdata}\fastget"

[Code]
// JSONEscapePath doubles the backslashes in a Windows path for embedding in JSON.
function JSONEscapePath(const P: string): string;
begin
  Result := P;
  StringChangeEx(Result, '\', '\\', True);
end;

// WriteHostManifests writes the native-messaging host manifests with the real
// install path. Firefox keys on the add-on id (allowed_extensions); Chrome/Edge
// key on an extension id (allowed_origins) that the user pastes once for an
// unpacked install, or that is fixed when published to the Chrome Web Store.
procedure WriteHostManifests;
var
  Dir, Exe, Firefox, Chrome, ChromeId: string;
begin
  Dir := ExpandConstant('{app}\host');
  ForceDirectories(Dir);
  Exe := JSONEscapePath(ExpandConstant('{app}\fastget-host.exe'));

  Firefox :=
    '{'                                                              + #13#10 +
    '  "name": "{#HostName}",'                                       + #13#10 +
    '  "description": "fastget native messaging host",'             + #13#10 +
    '  "path": "' + Exe + '",'                                       + #13#10 +
    '  "type": "stdio",'                                             + #13#10 +
    '  "allowed_extensions": ["{#FirefoxAddonId}"]'                  + #13#10 +
    '}';
  SaveStringToFile(Dir + '\{#HostName}.firefox.json', Firefox, False);

  // Chrome/Edge: the extension id is only known once the extension is loaded or
  // published. We write a manifest with a placeholder the user can replace, and
  // the setup page explains it. (Firefox works out of the box.)
  ChromeId := 'REPLACE_WITH_YOUR_CHROME_EXTENSION_ID';
  Chrome :=
    '{'                                                              + #13#10 +
    '  "name": "{#HostName}",'                                       + #13#10 +
    '  "description": "fastget native messaging host",'             + #13#10 +
    '  "path": "' + Exe + '",'                                       + #13#10 +
    '  "type": "stdio",'                                             + #13#10 +
    '  "allowed_origins": ["chrome-extension://' + ChromeId + '/"]'  + #13#10 +
    '}';
  SaveStringToFile(Dir + '\{#HostName}.chrome.json', Chrome, False);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
    WriteHostManifests;
end;
