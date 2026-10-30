# addons.mozilla.org (AMO) submission text

Copy these into the AMO submission form. They explain the extension and justify
each permission, which is required when an add-on uses cookies / all-urls.

---

## Name
fastget Download Catcher

## Summary (short)
Send browser downloads to the free fastget app for fast multi-connection
downloading, pause/resume, and a download list. Requires the fastget app for
Windows.

## Description (long)
fastget speeds up downloads by splitting each file into several parts and
downloading them at the same time, then joining them back together. It can
pause and resume downloads, keep a download list, limit total speed, and let you
choose where each file is saved.

How it works: this extension catches a download starting in Firefox and hands it
to the fastget app, a small free, open-source program that runs on your own
computer and does the actual downloading. You install the app once (an installer
sets everything up, no command line). Without the app, the extension cannot
download on its own.

Requirements:
- Windows (the fastget app is currently Windows-only).
- The free fastget app, installed from the link on the project page:
  https://github.com/alphaxt/fastget

Privacy: everything stays on your computer. The extension sends a download's URL,
name and (optionally) that site's cookies only to the local app, over your
computer's internal loopback connection. Nothing is sent to the developer or any
third party. There are no trackers. See the privacy policy inside the extension
(privacy.html) or on the project page.

## Privacy policy URL
Host privacy.html on the project site, or paste the privacy.html text into AMO's
privacy policy field.

---

## Permission justifications (paste in the reviewer notes)

- downloads: to detect a download starting and cancel the browser's own copy so
  fastget can take over.
- nativeMessaging: to talk to the local fastget app that performs the download.
- storage: to save the user's settings on their device.
- notifications: to tell the user when a download finishes or fails, and when the
  app is not installed.
- cookies (optional): requested only when the user opts in, so downloads that
  require a login/membership keep working. The cookies of the download's own site
  are passed to the local app for that one download. Not requested by default.
- <all_urls> (optional host permission): paired with cookies above, needed to
  read the download site's cookies. Requested only when the user opts in.

## Reviewer notes
This add-on is the browser half of a download manager. The companion app
(open source, link above) must be installed; its installer registers the native
messaging host. If the app is not installed, the extension does not break the
browser: it hands the download back to Firefox and opens a one-page setup guide.
The optional cookies/all-urls permissions are not requested on install; the user
grants them from the setup page only if they need logged-in downloads.

## Note on distribution
If a public listing is not desired, the same package can be submitted as
"unlisted" (self-distribution): Mozilla signs it and you install the signed .xpi
yourself, without a public store page.
