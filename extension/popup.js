// popup.js — toolbar popup: open the download list and edit settings.

const DEFAULTS = { enabled: true, connections: 8, minSizeBytes: 0, askEachTime: true, showProgress: true };

const enabledEl = document.getElementById("enabled");
const askEl = document.getElementById("ask");
const progEl = document.getElementById("showProgress");
const connEl = document.getElementById("connections");
const minSizeEl = document.getElementById("minSize");
const listBtn = document.getElementById("openList");

chrome.storage.local.get(DEFAULTS).then((s) => {
  enabledEl.checked = !!s.enabled;
  askEl.checked = !!s.askEachTime;
  progEl.checked = !!s.showProgress;
  connEl.value = s.connections;
  minSizeEl.value = Math.round((s.minSizeBytes || 0) / (1024 * 1024));
});

// Show the setup prompt only when the app is not detected.
const setupBox = document.getElementById("setupBox");
chrome.runtime.sendMessage({ cmd: "checkApp" }, (r) => {
  setupBox.hidden = !!(r && r.ok);
});
document.getElementById("setup").addEventListener("click", () => {
  chrome.tabs.create({ url: chrome.runtime.getURL("setup.html") });
  window.close();
});

// Optional: cookies + host access, only for logged-in / members-only downloads.
const COOKIES = { permissions: ["cookies"], origins: ["<all_urls>"] };
const permBox = document.getElementById("permBox");
chrome.permissions.contains(COOKIES).then((ok) => { permBox.hidden = ok; });
document.getElementById("grant").addEventListener("click", async () => {
  let ok = false;
  try { ok = await chrome.permissions.request(COOKIES); } catch {}
  permBox.hidden = ok;
});

listBtn.addEventListener("click", () => {
  listBtn.disabled = true;
  listBtn.textContent = "Opening…";
  // The background worker asks the native host to start fastget if needed.
  chrome.runtime.sendMessage({ cmd: "openList" }, () => window.close());
});

enabledEl.addEventListener("change", () => {
  chrome.storage.local.set({ enabled: enabledEl.checked });
});

progEl.addEventListener("change", () => {
  chrome.storage.local.set({ showProgress: progEl.checked });
});

askEl.addEventListener("change", () => {
  chrome.storage.local.set({ askEachTime: askEl.checked });
});

connEl.addEventListener("change", () => {
  let n = parseInt(connEl.value, 10);
  if (!Number.isFinite(n) || n < 1) n = 1;
  if (n > 32) n = 32;
  connEl.value = n;
  chrome.storage.local.set({ connections: n });
});

minSizeEl.addEventListener("change", () => {
  let mb = parseInt(minSizeEl.value, 10);
  if (!Number.isFinite(mb) || mb < 0) mb = 0;
  minSizeEl.value = mb;
  chrome.storage.local.set({ minSizeBytes: mb * 1024 * 1024 });
});
