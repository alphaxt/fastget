// setup.js — drives the one-page setup guide.

const statusEl = document.getElementById("status");
const grantBtn = document.getElementById("grant");
const grantStatus = document.getElementById("grantStatus");

// Poll the app until it is reachable, so the status updates as soon as the
// user finishes installing.
async function checkApp() {
  const r = await new Promise((resolve) =>
    chrome.runtime.sendMessage({ cmd: "checkApp" }, resolve)
  );
  if (r && r.ok) {
    statusEl.textContent = "The fastget app is installed and running. You're all set.";
    statusEl.className = "status ok";
  } else {
    statusEl.textContent = "The app isn't detected yet. Install it, then this will update automatically.";
    statusEl.className = "status no";
    setTimeout(checkApp, 3000);
  }
}
checkApp();

// Optional cookies permission for logged-in downloads.
const COOKIES = { permissions: ["cookies"], origins: ["<all_urls>"] };

async function refreshGrant() {
  const has = await chrome.permissions.contains(COOKIES);
  grantStatus.textContent = has
    ? "Granted. fastget can download logged-in files."
    : "Not granted. Public downloads work without this.";
  grantStatus.className = "status " + (has ? "ok" : "no");
  grantBtn.style.display = has ? "none" : "inline-block";
}
refreshGrant();

grantBtn.addEventListener("click", async (e) => {
  e.preventDefault();
  try {
    await chrome.permissions.request(COOKIES);
  } catch {}
  refreshGrant();
});
