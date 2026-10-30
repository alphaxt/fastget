# fastget on Firefox

## How it works

1. Click any download link in Firefox.
2. The **fastget** popup asks: *Download with fastget / Use browser / Cancel*.
   You can rename the file, pick the folder with **Browse…** (any drive, e.g.
   H:), see the free space there, tick **Always save here** to make it the
   default, and change the number of connections.
3. Click **Download with fastget**: a live progress window opens with progress,
   speed, time left, **Pause / Resume / Cancel**, and **Open / Open folder** when
   it finishes.
4. All downloads are also in the **download list** (toolbar button →
   *Open download list*, or the fastget tray icon → *Open fastget*).
   Downloads keep running after you close Firefox.

First time only: click the fastget toolbar button. If it shows
**Allow access to websites**, click it. Without that permission Firefox will not
give fastget a site's cookies, and some downloads fail with "403".

## Installing — two ways

The native host is already registered on this PC
(`register-host-firefox.ps1` has been run; add-on ID `fastget@fastget.addon`).

## A. Use it right now (temporary — resets when Firefox closes)

1. Open Firefox, go to:  `about:debugging`
2. Click **This Firefox**
3. Click **Load Temporary Add-on…**
4. Pick:  `e:\reverse\downloader\extension\manifest.json`

Click a download link to test. Redo these steps after each Firefox restart.

## B. Permanent (signed by Mozilla, for your own use)

Firefox only installs permanently if Mozilla has signed the add-on. Signing is
free. Use the "unlisted / self-distribution" track so it is NOT public — only you
install it.

Ready-to-upload package:  `e:\reverse\downloader\build\fastget-extension.zip`

Steps:

1. Create a free account at https://addons.mozilla.org (AMO).
2. Go to the Developer Hub:  https://addons.mozilla.org/developers/
3. Click **Submit a New Add-on**.
4. Choose **"On your own"** (this is the *unlisted* / self-distribution option —
   it gets signed but is not listed publicly).
5. Upload `build\fastget-extension.zip`.
6. Mozilla validates and signs it, then gives you a signed **.xpi** file to
   download (usually within a minute or two).
7. Install the signed .xpi permanently:
   - Open Firefox → `about:addons`
   - Click the gear ⚙ → **Install Add-on From File…**
   - Select the signed **.xpi** you downloaded.

It now stays installed across restarts, like IDM's add-on.

### Notes
- The add-on ID (`fastget@fastget.addon`) must stay the same, because the native
  host's `allowed_extensions` is locked to it. Don't change it.
- If Mozilla's validator complains about the `service_worker` line in
  `manifest.json`, remove just that one line (keep `"scripts": ["background.js"]`)
  and re-zip. Firefox uses `scripts`; `service_worker` is only for Chrome.
- The native host registration on this PC already matches, so once the signed
  add-on is installed, download catching works immediately.
