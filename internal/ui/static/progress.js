// progress.js — live IDM-style progress window for a single download.
//
// Polls GET /api/jobs/{id} and renders progress, speed, time left and status.
// Pause / Resume / Cancel call the job actions; once finished it switches to a
// completion view with Open / Open folder.

const id = new URLSearchParams(location.search).get("id");
const $ = (s) => document.getElementById(s);

let lastBytes = null;
let lastTime = 0;
let smoothSpeed = 0;
let closingAfterCancel = false;

function human(n) {
  if (!n || n <= 0) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let v = n, i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i ? (v < 10 ? 2 : 1) : 0)} ${u[i]}`;
}

function duration(sec) {
  if (!isFinite(sec) || sec < 0) return "—";
  sec = Math.round(sec);
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  if (h) return `${h} h ${m} min`;
  if (m) return `${m} min ${s} sec`;
  return `${s} sec`;
}

function baseName(p) {
  return p ? p.split(/[\\/]/).pop() : "";
}

const statusText = {
  queued: "Waiting for a free slot",
  scheduled: "Scheduled",
  running: "Receiving data…",
  paused: "Paused",
  done: "Download complete",
  error: "Error",
};

async function call(action) {
  const res = await fetch(`/api/jobs/${encodeURIComponent(id)}/${action}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
  if (!res.ok) {
    let msg = `request failed (${res.status})`;
    try { msg = (await res.json()).error || msg; } catch {}
    showError(msg);
  }
  return res.ok;
}

function showError(msg) {
  const el = $("error");
  el.textContent = msg;
  el.hidden = !msg;
}

function render(j) {
  const name = baseName(j.output_path) || "download";
  const pct = j.total_size > 0 ? Math.min(100, (j.downloaded / j.total_size) * 100) : (j.status === "done" ? 100 : 0);

  document.title = j.status === "done" ? `Done — ${name}` : `${Math.floor(pct)}% ${name}`;
  $("name").textContent = name;
  $("name").title = name;
  $("url").textContent = j.url;
  $("url").title = j.url;
  $("bar").style.width = `${pct}%`;
  $("pct").textContent = `${pct.toFixed(1)}%`;
  $("state").textContent = j.status;
  $("status").textContent = statusText[j.status] || j.status;
  $("size").textContent = j.total_size > 0 ? human(j.total_size) : "unknown";
  $("done").textContent = j.total_size > 0
    ? `${human(j.downloaded)} (${pct.toFixed(1)}%)`
    : human(j.downloaded);
  $("conns").textContent = j.connections || "—";
  $("path").textContent = j.output_path || "—";

  // Speed: prefer the engine's sample, smoothed; fall back to our own delta.
  const now = performance.now();
  let speed = j.speed_bps || 0;
  if (!speed && lastBytes !== null && now > lastTime) {
    speed = ((j.downloaded - lastBytes) / (now - lastTime)) * 1000;
  }
  lastBytes = j.downloaded;
  lastTime = now;
  smoothSpeed = smoothSpeed ? smoothSpeed * 0.6 + speed * 0.4 : speed;

  const running = j.status === "running";
  $("speed").textContent = running ? `${human(smoothSpeed)}/s` : "—";
  $("eta").textContent = running && j.total_size > 0 && smoothSpeed > 0
    ? duration((j.total_size - j.downloaded) / smoothSpeed)
    : (j.status === "done" ? "0 sec" : "—");

  document.body.className =
    j.status === "done" ? "done" : j.status === "paused" ? "paused" : j.status === "error" ? "failed" : "";

  const finished = j.status === "done";
  $("runningActions").hidden = finished;
  $("doneActions").hidden = !finished;
  const canResume = j.status === "paused" || j.status === "error";
  $("changeFolder").hidden = j.status === "running" || j.status === "done";
  $("pause").hidden = canResume;
  $("resume").hidden = !canResume;
  $("resume").textContent = j.status === "error" ? "Retry" : "Resume";

  if (j.status === "error") showError(j.last_error || "The download failed.");
  else if ($("error").textContent && !$("error").dataset.sticky) showError("");
}

async function poll() {
  if (!id) {
    $("name").textContent = "No download selected";
    return;
  }
  try {
    const res = await fetch(`/api/jobs/${encodeURIComponent(id)}`, { cache: "no-store" });
    if (res.status === 404) {
      if (closingAfterCancel) { window.close(); return; }
      $("name").textContent = "This download was removed";
      $("runningActions").hidden = true;
      $("doneActions").hidden = true;
      return;
    }
    if (res.ok) render(await res.json());
  } catch {
    $("state").textContent = "fastget is not running";
  }
  setTimeout(poll, 700);
}

// Change folder: fastget opens the Windows folder dialog, moves the download
// there and starts it again.
$("changeFolder").onclick = async () => {
  const btn = $("changeFolder");
  btn.disabled = true;
  btn.textContent = "Choose a folder…";
  try {
    const res = await fetch(`/api/jobs/${encodeURIComponent(id)}/move`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) showError(data.error || `could not change folder (${res.status})`);
    else if (!data.cancelled) showError("");
  } finally {
    btn.disabled = false;
    btn.textContent = "Change folder…";
    window.focus();
  }
};

$("pause").onclick = () => call("pause");
$("resume").onclick = () => call("resume");
$("cancel").onclick = async () => {
  if (!confirm("Cancel this download and delete the partial file?")) return;
  closingAfterCancel = true;
  if (await call("remove-file")) window.close();
};
$("open").onclick = () => call("open");
$("openFolder").onclick = () => call("open-folder");
$("close").onclick = () => window.close();
$("list").onclick = $("list2").onclick = () => window.open("/", "fastget-list");

poll();
