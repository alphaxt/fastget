# fastget browser extension

Catches downloads in Chrome, Edge and Firefox and hands them to the local
fastget app, which does the fast multi-connection downloading. This is the
"the download manager pops up and grabs the download" behaviour, built on the
browser's own supported extension + native-messaging APIs.

Project: https://github.com/alphaxt/fastget

## How it fits together

```
Browser download starts
        │
        ▼
 WebExtension (background.js)     cancels the browser download, shows the
        │                         "download this file?" popup, gathers URL/
        │                         name/size/referrer (+ cookies if allowed)
        ▼  native messaging (4-byte length + JSON frames)
 fastget-host.exe                 hands it to the running app (starts it if needed)
        │
        ▼
 fastget-ui.exe (engine + queue)  multi-connection download, shown in the
        │                         download list + live progress window
        ▼
 file saved to the folder you chose
```

The browser only launches `fastget-host.exe` because its manifest is registered
on the machine and lists this extension's ID. Arbitrary web pages cannot reach it.

## For end users

Install the app from the
[latest release](https://github.com/alphaxt/fastget/releases/latest). The
installer registers the native host for all installed browsers automatically.
Then add the extension from the browser's store. On first use, the extension's
setup page confirms the app is detected; for logged-in/members-only downloads you
can optionally grant cookie access from that page.

The extension is Windows-only in practice, because the app it drives is
Windows-only.

## For developers (loading the unpacked extension)

### 1. Build

```powershell
.\build\build.ps1 -Go "C:\Program Files\Go\bin\go.exe"
```

This produces the binaries in `build\dist` and a browser-ready copy of the
extension in `build\dist\extension` (web files only — the `host\` scripts below
are not part of the shipped extension).

### 2. Register the native host (dev convenience scripts)

```powershell
# Firefox (add-on id is fixed: fastget@fastget.addon)
.\extension\host\register-host-firefox.ps1

# Chrome / Edge (needs the id the browser assigns to your unpacked load)
.\extension\host\register-host.ps1 -ExtensionId <PASTE_THE_ID>
```

These write the host manifest with the real path to `fastget-host.exe` and the
registry keys under `HKCU`. The packaged installer does the same thing without
scripts; these are only for development.

### 3. Load the extension

- **Chrome/Edge:** `chrome://extensions` → Developer mode → **Load unpacked** →
  select `extension` (or `build\dist\extension`). Copy the extension ID and use
  it in step 2.
- **Firefox:** `about:debugging#/runtime/this-firefox` → **Load Temporary
  Add-on** → select `extension/manifest.json`. (Temporary add-ons reset on
  restart; a permanent install must be signed — see `FIREFOX-SETUP.md`.)

### 4. Test

Click a download link. The confirm popup appears; choose a folder and start. The
file lands where you chose, with a live progress window and a row in the
download list.

## Files

| File                    | Purpose                                             |
|-------------------------|-----------------------------------------------------|
| `manifest.json`         | MV3 manifest; gecko id `fastget@fastget.addon`      |
| `background.js`         | catches downloads, talks to the host                |
| `popup.html/.js`        | toolbar popup: open list, settings, setup prompt    |
| `dialog.html/.js`       | the "download this file?" confirm popup             |
| `setup.html/.js`        | one-page setup / app-detection guide                |
| `privacy.html`          | privacy policy (for the store listing)              |
| `AMO-LISTING.md`        | addons.mozilla.org submission text + justifications |
| `host/`                 | dev-only host registration scripts (not shipped)    |

## Troubleshooting

- **"fastget app isn't installed" setup page opens** — install the app, or
  (dev) re-run the register-host script and restart the browser.
- **Download happens in the browser anyway** — catching may be off in the popup,
  the file may be under the size threshold, or it's a non-http(s) URL (blob:),
  which is intentionally left to the browser. Also, when the app is missing the
  extension deliberately hands the download back to the browser so nothing is
  lost.
- **A logged-in download fails (403)** — grant cookie access from the setup page
  or the toolbar popup.
