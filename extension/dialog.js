// dialog.js — the IDM-style "download this file?" popup.
//
// Shows the caught download, lets the user rename it, choose the folder (with a
// native Browse… dialog and a free-space check), and then reports the choice:
// download with fastget, use the browser, or cancel.

const id = new URLSearchParams(location.search).get("id");
const $ = (s) => document.getElementById(s);

let info = null;
let fileSize = 0;

function human(n) {
  if (!n || n <= 0) return "unknown";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function send(msg) {
  return new Promise((resolve) => chrome.runtime.sendMessage(msg, resolve));
}

// showFree shows the free space for the chosen folder and warns if the file
// will not fit.
function showFree(reply) {
  const el = $("free");
  el.className = "sub";
  if (!reply || reply.type !== "folder") {
    el.textContent = reply && reply.message ? reply.message : "";
    if (reply && reply.type === "error") el.className = "sub err";
    return;
  }
  if (reply.freeBytes == null) {
    el.textContent = "";
    return;
  }
  if (fileSize > 0 && fileSize > reply.freeBytes) {
    el.className = "sub err";
    el.textContent = `Not enough space: ${human(reply.freeBytes)} free, file needs ${human(fileSize)}. Choose another drive.`;
  } else {
    el.textContent = `${human(reply.freeBytes)} free on this drive`;
  }
}

async function refreshFree() {
  const folder = $("folder").value.trim();
  if (!folder) { $("free").textContent = ""; return; }
  showFree(await send({ cmd: "folderInfo", folder }));
}

async function load() {
  info = await send({ cmd: "getInfo", id });
  if (!info) {
    $("file").value = "(this download is no longer available)";
    $("file").disabled = true;
    $("download").disabled = true;
    return;
  }
  fileSize = info.size || 0;
  $("file").value = info.filename || "";
  $("size").textContent = human(fileSize);
  $("url").textContent = info.url;
  if (info.connections) $("connections").value = info.connections;
  $("file").focus();
  const dot = $("file").value.lastIndexOf(".");
  $("file").setSelectionRange(0, dot > 0 ? dot : $("file").value.length);

  // Ask fastget which folder to use (starts fastget if it is not running).
  const reply = await send({ cmd: "defaultFolder", id });
  if (reply && reply.type === "folder") {
    $("folder").value = reply.folder;
    showFree(reply);
  } else {
    $("folder").placeholder = "Downloads folder";
    showFree(reply);
  }
}

$("browse").addEventListener("click", async () => {
  $("browse").disabled = true;
  $("browse").textContent = "Choosing…";
  const reply = await send({ cmd: "pickFolder", initial: $("folder").value.trim() });
  $("browse").disabled = false;
  $("browse").textContent = "Browse…";
  if (reply && reply.type === "folder") {
    $("folder").value = reply.folder;
    $("remember").checked = true; // picking a folder usually means "use this from now on"
    showFree(reply);
  } else if (reply && reply.type === "error") {
    showFree(reply);
  }
  window.focus();
});

$("folder").addEventListener("change", refreshFree);

let decided = false;
function decide(action) {
  if (decided) return;
  decided = true;
  chrome.runtime.sendMessage({
    cmd: "decide",
    id,
    action,
    connections: parseInt($("connections").value, 10) || 8,
    filename: $("file").value.trim(),
    folder: $("folder").value.trim(),
    remember: $("remember").checked,
  }, () => window.close());
}

$("download").addEventListener("click", () => decide("download"));
$("browser").addEventListener("click", () => decide("browser"));
$("cancel").addEventListener("click", () => decide("cancel"));

document.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && e.target.tagName !== "BUTTON" && e.target.id !== "folder") decide("download");
  if (e.key === "Escape") decide("cancel");
});

load();
