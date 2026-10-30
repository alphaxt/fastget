// app.js — drives the fastget download list.
//
// It polls /api/jobs for the current state and renders a row per download, with
// a progress bar, live speed, status badge, and per-job controls. Adding a URL
// and the pause/resume/remove buttons POST to the small JSON API.

const bodyEl = document.getElementById("jobsBody");
const emptyEl = document.getElementById("empty");
const addForm = document.getElementById("addForm");
const urlEl = document.getElementById("url");
const connEl = document.getElementById("connections");
const maxConcEl = document.getElementById("maxConcurrent");

const startAtEl = document.getElementById("startAt");

addForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const url = urlEl.value.trim();
  if (!url) return;
  const payload = {
    url,
    connections: parseInt(connEl.value, 10) || 8,
  };
  // datetime-local gives local wall-clock with no zone; convert to RFC3339.
  if (startAtEl.value) {
    const d = new Date(startAtEl.value);
    if (!isNaN(d.getTime())) payload.startAt = d.toISOString();
  }
  await api("/api/jobs", "POST", payload);
  urlEl.value = "";
  startAtEl.value = "";
  refresh();
});

// --- allowed-time window ---
const winEnabledEl = document.getElementById("windowEnabled");
const winStartEl = document.getElementById("windowStart");
const winEndEl = document.getElementById("windowEnd");
const winSaveEl = document.getElementById("windowSave");

function hhmmToMin(s) {
  const m = /^(\d{1,2}):(\d{2})$/.exec(s || "");
  if (!m) return 0;
  return (parseInt(m[1], 10) % 24) * 60 + (parseInt(m[2], 10) % 60);
}
function minToHHMM(min) {
  const h = Math.floor(min / 60) % 24;
  const m = min % 60;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`;
}

winSaveEl.addEventListener("click", async () => {
  await api("/api/config", "POST", {
    window: {
      enabled: winEnabledEl.checked,
      startMin: hhmmToMin(winStartEl.value),
      endMin: hhmmToMin(winEndEl.value),
    },
  });
});

// --- default save folder ---
const saveFolderEl = document.getElementById("saveFolder");
const saveFreeEl = document.getElementById("saveFree");

function showFolder(cfg) {
  saveFolderEl.textContent = cfg.downloadFolder || "Downloads";
  saveFreeEl.textContent = cfg.freeBytes != null ? `(${humanBytes(cfg.freeBytes)} free)` : "";
}

document.getElementById("folderChange").addEventListener("click", async (e) => {
  e.target.disabled = true;
  try {
    // fastget opens the Windows folder dialog and returns the choice.
    const picked = await api("/api/pick-folder", "POST", {});
    if (!picked.cancelled && picked.folder) {
      await api("/api/config", "POST", { downloadFolder: picked.folder });
      showFolder(await api("/api/config", "GET"));
    }
  } finally {
    e.target.disabled = false;
  }
});

document.getElementById("folderReset").addEventListener("click", async () => {
  await api("/api/config", "POST", { downloadFolder: "" });
  showFolder(await api("/api/config", "GET"));
});

// Load the current settings on startup.
(async () => {
  try {
    const cfg = await api("/api/config", "GET");
    if (cfg.window) {
      winEnabledEl.checked = !!cfg.window.enabled;
      winStartEl.value = minToHHMM(cfg.window.startMin || 0);
      winEndEl.value = minToHHMM(cfg.window.endMin || 0);
    }
    showFolder(cfg);
    // Show the saved settings instead of the page defaults.
    if (cfg.maxConcurrent) maxConcEl.value = cfg.maxConcurrent;
    if (cfg.maxBytesPerSec > 0) speedEl.value = formatRate(cfg.maxBytesPerSec);
  } catch {}
})();

function formatRate(bps) {
  if (bps % (1024 * 1024) === 0) return `${bps / (1024 * 1024)}m`;
  if (bps % 1024 === 0) return `${bps / 1024}k`;
  return String(bps);
}

maxConcEl.addEventListener("change", async () => {
  const n = parseInt(maxConcEl.value, 10) || 1;
  await api("/api/config", "POST", { maxConcurrent: n });
});

const speedEl = document.getElementById("speedLimit");
speedEl.addEventListener("change", async () => {
  const bps = parseRate(speedEl.value);
  if (bps === null) {
    showMessage("Speed limit must be a number with k or m (for example 500k or 2m), or empty for no limit.");
    return;
  }
  await api("/api/config", "POST", { maxBytesPerSec: bps });
});

// parseRate converts "500k" / "2m" / "" into bytes/sec. Returns null on invalid
// input, 0 for empty (unlimited).
function parseRate(s) {
  s = (s || "").trim().toLowerCase();
  if (s === "" || s === "0") return 0;
  let mult = 1;
  const m = s.match(/^([0-9]*\.?[0-9]+)\s*(k|kb|m|mb|g|gb|b)?$/);
  if (!m) return null;
  const val = parseFloat(m[1]);
  switch (m[2]) {
    case "k": case "kb": mult = 1024; break;
    case "m": case "mb": mult = 1024 * 1024; break;
    case "g": case "gb": mult = 1024 * 1024 * 1024; break;
    default: mult = 1;
  }
  return Math.round(val * mult);
}

// api calls the fastget API. Errors are shown in the page's message bar, never
// in an alert box (the list polls constantly; alerts would keep popping up).
async function api(path, method, body, quiet) {
  const opts = { method, headers: { "Content-Type": "application/json" }, cache: "no-store" };
  if (body !== undefined) opts.body = JSON.stringify(body);
  const res = await fetch(path, opts);
  let data = {};
  try {
    data = await res.json();
  } catch {}
  if (!res.ok) {
    const msg = data.error || `request failed (${res.status})`;
    if (!quiet) showMessage(msg);
    throw new Error(msg);
  }
  return data;
}

const msgEl = document.getElementById("message");
let msgTimer = 0;
function showMessage(text) {
  msgEl.textContent = text;
  msgEl.hidden = !text;
  clearTimeout(msgTimer);
  if (text) msgTimer = setTimeout(() => { msgEl.hidden = true; }, 8000);
}

function humanBytes(n) {
  if (!n || n < 1024) return `${n || 0} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1)} ${units[i]}`;
}

function fileName(p) {
  if (!p) return "(pending)";
  return p.split(/[\\/]/).pop();
}

function statusBadge(status) {
  return `<span class="status ${status}">${status}</span>`;
}

function actionButtons(job) {
  const btn = (action, label) =>
    `<button class="secondary" data-id="${job.id}" data-action="${action}">${label}</button>`;
  const parts = [btn("details", "Details")];
  if (job.status === "running" || job.status === "queued" || job.status === "scheduled") parts.push(btn("pause", "Pause"));
  if (job.status === "paused" || job.status === "error") {
    parts.push(btn("resume", job.status === "error" ? "Retry" : "Resume"));
    parts.push(btn("move", "Move…"));
  }
  if (job.status === "done") {
    parts.push(btn("open", "Open"));
    parts.push(btn("open-folder", "Folder"));
  }
  parts.push(btn("remove", "Remove"));
  return `<div class="actions">${parts.join("")}</div>`;
}

// openProgress opens the IDM-style live progress window for one download.
function openProgress(id) {
  window.open(`/progress?id=${encodeURIComponent(id)}`, `fg-${id}`,
    "popup=yes,width=540,height=430");
}

// Clicking a row's file name also opens its progress window.
bodyEl.addEventListener("dblclick", (e) => {
  const row = e.target.closest("tr[data-id]");
  if (row) openProgress(row.dataset.id);
});

// Rows are created once per download and then updated in place. Rebuilding
// the table on every poll replaced buttons under the mouse (lost clicks) and
// made the page flicker.
const rows = new Map(); // job id -> { tr, cells, status, actionsKey }

function createRow(job) {
  const tr = document.createElement("tr");
  tr.dataset.id = job.id;
  tr.innerHTML = `
    <td class="file"><span class="fname"></span><div class="err-msg" hidden></div></td>
    <td class="size"></td>
    <td class="progress-col">
      <div class="bar"><span></span></div>
      <span class="pct"></span>
    </td>
    <td class="speed"></td>
    <td class="st"></td>
    <td class="act"></td>`;
  const q = (s) => tr.querySelector(s);
  return {
    tr,
    name: q(".fname"), err: q(".err-msg"), file: q(".file"), size: q(".size"),
    bar: q(".bar > span"), pct: q(".pct"), speed: q(".speed"), st: q(".st"), act: q(".act"),
    actionsKey: "",
  };
}

// set changes a text/attribute only when it differs, so the browser does not
// redraw (or reset hover state on) unchanged parts.
function setText(el, v) { if (el.textContent !== v) el.textContent = v; }

function updateRow(r, job) {
  const pct = Math.max(0, Math.min(100, job.percent || 0));
  setText(r.name, fileName(job.output_path));
  const tip = job.output_path || job.url;
  if (r.file.title !== tip) r.file.title = tip;
  setText(r.err, job.last_error || "");
  r.err.hidden = !job.last_error;
  setText(r.size, job.total_size > 0 ? humanBytes(job.total_size) : "—");
  const w = `${pct}%`;
  if (r.bar.style.width !== w) r.bar.style.width = w;
  setText(r.pct, `${pct.toFixed(1)}% · ${humanBytes(job.downloaded)}`);
  setText(r.speed, job.status === "running" ? `${humanBytes(job.speed_bps)}/s` : "—");
  if (r.st.dataset.status !== job.status) {
    r.st.dataset.status = job.status;
    r.st.innerHTML = statusBadge(job.status);
  }
  // Buttons only change when the status changes.
  const key = job.status;
  if (r.actionsKey !== key) {
    r.actionsKey = key;
    r.act.innerHTML = actionButtons(job);
  }
}

const totalEl = document.getElementById("totalSpeed");

function render(jobs) {
  const seen = new Set();
  let total = 0, active = 0;
  jobs.forEach((job, i) => {
    seen.add(job.id);
    let r = rows.get(job.id);
    if (!r) {
      r = createRow(job);
      rows.set(job.id, r);
    }
    updateRow(r, job);
    // Keep the table in the server's order without moving rows needlessly.
    if (bodyEl.children[i] !== r.tr) bodyEl.insertBefore(r.tr, bodyEl.children[i] || null);
    if (job.status === "running") {
      active++;
      total += job.speed_bps || 0;
    }
  });
  for (const [id, r] of rows) {
    if (!seen.has(id)) {
      r.tr.remove();
      rows.delete(id);
    }
  }
  emptyEl.style.display = jobs.length ? "none" : "block";
  setText(totalEl, active ? `${active} downloading · ${humanBytes(total)}/s total` : "");
}

function escapeHtml(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

// Delegate action-button clicks.
bodyEl.addEventListener("click", async (e) => {
  const btn = e.target.closest("button[data-action]");
  if (!btn) return;
  const { id, action } = btn.dataset;
  if (action === "details") {
    openProgress(id);
    return;
  }
  btn.disabled = true;
  try {
    await api(`/api/jobs/${id}/${action}`, "POST", {});
  } catch {
    // message already shown
  } finally {
    btn.disabled = false;
  }
  refresh();
});

let offline = false;
async function refresh() {
  try {
    const data = await api("/api/jobs", "GET", undefined, true);
    render(data.jobs || []);
    if (offline) { offline = false; showMessage(""); }
  } catch {
    if (!offline) { offline = true; showMessage("fastget is not responding. Is it still running?"); }
  }
}

// Poll for live progress. Each poll waits for the previous one to finish, so a
// slow moment never piles up requests. Hidden tabs poll less often.
async function pollLoop() {
  await refresh();
  setTimeout(pollLoop, document.hidden ? 3000 : 1000);
}
pollLoop();
