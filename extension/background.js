// background.js — fastget download catcher (IDM-style).
//
// Flow when a download starts in the browser:
//   1. cancel the browser's own download,
//   2. collect URL, file name, size, referrer and the cookies for that URL,
//   3. show the "download this file?" dialog (dialog.html),
//   4. on "Download", hand it to the native host, which queues it in the fastget
//      desktop app (starting it in the tray if needed), then
//   5. open that download's live progress window (progress, speed, time left,
//      pause / resume / cancel, open / open folder when done).
//
// The native host is reached by its registered name; the browser only allows it
// because the host manifest lists this extension's ID.

const HOST_NAME = "com.fastget.host";

const DEFAULTS = { enabled: true, connections: 8, minSizeBytes: 0, askEachTime: true, showProgress: true };

let settings = { ...DEFAULTS };
chrome.storage.local.get(DEFAULTS).then((s) => { settings = { ...DEFAULTS, ...s }; });
chrome.storage.onChanged.addListener((changes) => {
  for (const [k, { newValue }] of Object.entries(changes)) {
    if (k in settings) settings[k] = newValue;
  }
});

// Catches waiting for the user's decision in the dialog, keyed by a short id.
const pending = new Map();
// URLs the user chose to download with the browser; skip catching them once.
const bypass = new Set();

// askHost sends one request to the native host and resolves with its reply.
function askHost(payload) {
  return new Promise((resolve) => {
    let port;
    try {
      port = chrome.runtime.connectNative(HOST_NAME);
    } catch (e) {
      resolve({ type: "error", message: "native host unavailable: " + e.message });
      return;
    }
    let answered = false;
    port.onMessage.addListener((msg) => {
      answered = true;
      resolve(msg);
      port.disconnect();
    });
    port.onDisconnect.addListener(() => {
      if (!answered) {
        const err = chrome.runtime.lastError && chrome.runtime.lastError.message;
        resolve({
          type: "error",
          message: "fastget native host is not reachable. Run extension\\host\\register-host-firefox.ps1 (or register-host.ps1 for Chrome/Edge). " + (err || ""),
        });
      }
    });
    port.postMessage(payload);
  });
}

chrome.downloads.onCreated.addListener(async (item) => {
  if (!settings.enabled) return;
  const url = item.finalUrl || item.url || "";
  if (!/^https?:/i.test(url)) return; // blob:, data:, file: stay with the browser
  if (bypass.delete(url)) return; // user picked "Use browser" for this one
  if (settings.minSizeBytes > 0 && item.fileSize > 0 && item.fileSize < settings.minSizeBytes) {
    return;
  }

  try {
    await chrome.downloads.cancel(item.id);
    await chrome.downloads.erase({ id: item.id });
  } catch {
    return; // already finished or not cancellable: leave it to the browser
  }

  const info = {
    id: Math.random().toString(36).slice(2, 10),
    url,
    filename: guessName(item),
    size: item.fileSize > 0 ? item.fileSize : (item.totalBytes > 0 ? item.totalBytes : 0),
    referrer: item.referrer || "",
    cookie: await gatherCookies(url),
    userAgent: navigator.userAgent,
    connections: settings.connections,
    // The folder Firefox itself would have saved to (its "Save files to"
    // setting or the folder picked in its own dialog).
    browserFolder: folderOf(item.filename),
    folder: "",
  };

  if (settings.askEachTime) {
    openDialog(info);
  } else {
    info.folder = (await chooseFolder(info)).folder || "";
    startDownload(info);
  }
});

// folderOf returns the folder part of an absolute file path, or "".
function folderOf(p) {
  if (!p || !(/^[a-zA-Z]:[\\/]/.test(p) || p.startsWith("/") || p.startsWith("\\\\"))) return "";
  const i = Math.max(p.lastIndexOf("\\"), p.lastIndexOf("/"));
  if (i <= 0) return "";
  const dir = p.slice(0, i);
  return /^[a-zA-Z]:$/.test(dir) ? dir + "\\" : dir;
}

// chooseFolder picks the folder to offer: the default set in fastget wins;
// otherwise the folder Firefox would use; otherwise fastget's fallback
// (the Downloads folder).
async function chooseFolder(info) {
  const def = await askHost({ type: "default-folder" });
  if (def.type === "folder" && !def.folderSet && info.browserFolder) {
    const hint = await askHost({ type: "folder-info", folder: info.browserFolder });
    if (hint.type === "folder") return hint;
  }
  return def;
}

function openDialog(info) {
  pending.set(info.id, info);
  chrome.windows.create({
    url: chrome.runtime.getURL("dialog.html") + "?id=" + encodeURIComponent(info.id),
    type: "popup",
    width: 600,
    height: 380,
  });
}

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.cmd === "getInfo") {
    sendResponse(pending.get(msg.id) || null);
    return false;
  }
  if (msg.cmd === "decide") {
    const info = pending.get(msg.id);
    pending.delete(msg.id);
    if (info) {
      if (msg.action === "download") {
        if (msg.connections) info.connections = msg.connections;
        if (msg.filename) info.filename = msg.filename;
        info.folder = msg.folder || "";
        info.remember = !!msg.remember;
        startDownload(info);
      } else if (msg.action === "browser") {
        bypass.add(info.url);
        chrome.downloads.download({ url: info.url, filename: info.filename || undefined });
      }
    }
    sendResponse({ ok: true });
    return false;
  }
  if (msg.cmd === "defaultFolder") {
    const info = pending.get(msg.id) || {};
    chooseFolder(info).then(sendResponse);
    return true;
  }
  if (msg.cmd === "pickFolder") {
    askHost({ type: "pick-folder", folder: msg.initial || "" }).then(sendResponse);
    return true;
  }
  if (msg.cmd === "folderInfo") {
    askHost({ type: "folder-info", folder: msg.folder || "" }).then(sendResponse);
    return true;
  }
  if (msg.cmd === "openList") {
    askHost({ type: "open-ui" }).then((reply) => {
      if (reply.type === "ui") chrome.tabs.create({ url: reply.listUrl });
      else notify("fastget", reply.message || "could not open the download list");
      sendResponse(reply);
    });
    return true; // async response
  }
  return false;
});

// startDownload queues the download in fastget and opens its progress window.
async function startDownload(info) {
  const reply = await askHost({
    type: "download",
    url: info.url,
    filename: info.filename,
    folder: info.folder || "",
    remember: !!info.remember,
    referrer: info.referrer,
    cookie: info.cookie,
    userAgent: info.userAgent,
    connections: info.connections,
  });
  if (reply.type === "queued") {
    if (!settings.showProgress) {
      notify("Downloading with fastget", reply.outputPath || info.url);
      return;
    }
    chrome.windows.create({
      url: reply.progressUrl,
      type: "popup",
      width: 540,
      height: 440,
    });
  } else {
    notify("Download failed", reply.message || "unknown error");
  }
}

async function gatherCookies(url) {
  try {
    const cookies = await chrome.cookies.getAll({ url });
    return cookies.map((c) => `${c.name}=${c.value}`).join("; ");
  } catch {
    return "";
  }
}

function guessName(item) {
  if (item.filename) return item.filename.split(/[\\/]/).pop();
  try {
    const u = new URL(item.finalUrl || item.url);
    return decodeURIComponent(u.pathname.split("/").pop() || "") || "download.bin";
  } catch {
    return "download.bin";
  }
}

function notify(title, message) {
  try {
    chrome.notifications.create({
      type: "basic",
      iconUrl: "icons/icon128.png",
      title,
      message: String(message).slice(0, 300),
    });
  } catch {}
}
