# fastget browser integration

Catches downloads in Chrome/Edge and hands them to the local fastget engine for
fast multi-connection downloading. This is the "the download manager pops up and
grabs the download" behaviour, built entirely on the browser's own supported
extension + native-messaging APIs.

## How it fits together

```
Browser download starts
        │
        ▼
 WebExtension (background.js)      cancels the browser download,
        │                          grabs the URL + filename
        ▼  native messaging (4-byte length + JSON frames)
 fastget-host.exe                  reads the request from stdin
        │
        ▼
 internal/engine (Go)             multi-connection download + resume
        │
        ▼
 file saved to your Downloads folder
```

The browser will only launch `fastget-host.exe` because:
1. its manifest is registered in the Windows registry, and
2. that manifest lists your specific extension ID in `allowed_origins`.

Arbitrary web pages cannot reach the host.

## Install (Windows, Chrome or Edge)

### 1. Build the binaries

```powershell
cd e:\reverse\downloader
go build -o fastget.exe ./cmd/fastget
go build -o fastget-host.exe ./cmd/fastget-host
```

### 2. Load the extension (to get its ID)

1. Open `chrome://extensions` (or `edge://extensions`).
2. Turn on **Developer mode** (top-right).
3. Click **Load unpacked** and select the `e:\reverse\downloader\extension` folder.
4. Copy the **extension ID** shown on the extension's card.

> Note: icons are referenced in `manifest.json` but not shipped here. Either add
> `icons/icon48.png` and `icons/icon128.png`, or remove the `icons` and
> `default_popup` icon references from `manifest.json` if the browser complains.

### 3. Register the native host

```powershell
cd e:\reverse\downloader\extension\host
.\register-host.ps1 -ExtensionId <PASTE_THE_ID_HERE>
```

This writes a concrete `com.fastget.host.json` (with the real path to
`fastget-host.exe` and your extension ID) and registers it for Chrome and Edge
under `HKCU` (no admin needed).

### 4. Restart the browser and test

Restart Chrome/Edge, then click a download link for a reasonably large file. The
extension cancels the browser's own download, fastget takes over, and the file
lands in your **Downloads** folder. A notification reports completion.

Use the toolbar popup to toggle catching, set the number of connections, or only
catch files above a size threshold.

## Uninstall

```powershell
cd e:\reverse\downloader\extension\host
.\register-host.ps1 -Unregister
```

Then remove the unpacked extension from `chrome://extensions`.

## Firefox

Firefox is supported too. It differs from Chrome/Edge in two ways, both handled
by a separate script:

- the host manifest uses `allowed_extensions` (the add-on's gecko ID) instead of
  `allowed_origins`, and
- it registers under `HKCU\Software\Mozilla\NativeMessagingHosts\com.fastget.host`.

The add-on ID is fixed in `manifest.json`
(`browser_specific_settings.gecko.id` = `fastget@fastget.local`), so there's no
ID to copy the way there is for Chrome.

### Install on Firefox (Windows)

1. Build the binaries (same as Chrome, see above).
2. Register the host:
   ```powershell
   cd e:\reverse\downloader\extension\host
   .\register-host-firefox.ps1
   ```
   This writes `com.fastget.host.firefox.json` (kept separate from the Chrome
   manifest so both browsers can be registered at once) and the Firefox registry
   key.
3. Load the extension: open `about:debugging#/runtime/this-firefox`, click
   **Load Temporary Add-on**, and select `extension/manifest.json`. (Temporary
   add-ons are removed when Firefox restarts; for a permanent install the add-on
   must be signed, which is a separate step.)
4. Start a download to test.

Uninstall: `.\register-host-firefox.ps1 -Unregister`.

## Troubleshooting

- **"fastget host not reachable" notification** — the native host isn't
  registered, or the extension ID in the manifest doesn't match. Re-run
  `register-host.ps1` with the current ID and restart the browser.
- **Download happens in the browser anyway** — catching may be disabled in the
  popup, the file may be below the size threshold, or it's a non-http(s) URL
  (e.g. blob:), which is intentionally left to the browser.
- **Nothing is saved** — check that `fastget-host.exe` exists at the path written
  into `com.fastget.host.json`.
