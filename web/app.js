let currentConfig = null;
let armed = false;
let audioCtx = null;
let buffers = new Map();
let activeSounds = [];
let playbackGeneration = 0;
let replayClockText = "";
let replayClockUntil = 0;

const $ = (id) => document.getElementById(id);
const form = $("configForm");

async function fetchJSON(url, opts) {
  const res = await fetch(url, opts);
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

function fmt(n, digits = 0) {
  return Number(n || 0).toLocaleString(undefined, { maximumFractionDigits: digits, minimumFractionDigits: digits });
}

function setConfigToForm(cfg) {
  currentConfig = cfg;
  form.upThreshold.value = cfg.alerts.up.threshold;
  form.downThreshold.value = cfg.alerts.down.threshold;
  form.cooldown.value = cfg.alerts.cooldown_seconds;
  form.upMove.value = cfg.up_filter.move_pct;
  form.downMove.value = cfg.down_filter.move_pct;
  form.minVolume.value = cfg.calculation.min_today_volume;
  form.lookback.value = cfg.calculation.lookback_seconds;
  form.stale.value = cfg.calculation.max_stale_seconds;
  form.upSound.value = cfg.alerts.up.sound_file;
  form.downSound.value = cfg.alerts.down.sound_file;
  form.volume.value = cfg.alerts.volume;
  form.maxSounds.value = cfg.alerts.max_simultaneous_sounds;
  form.soundPolicy.value = cfg.alerts.on_sound_limit;
  form.masterMute.checked = cfg.alerts.master_mute;
  form.upMute.checked = cfg.alerts.up.mute;
  form.downMute.checked = cfg.alerts.down.mute;
  $("muteBtn").textContent = cfg.alerts.master_mute ? "Unmute" : "Mute";
  preloadSounds(cfg);
}

function formToConfig() {
  const cfg = structuredClone(currentConfig);
  cfg.alerts.up.threshold = Number(form.upThreshold.value);
  cfg.alerts.down.threshold = Number(form.downThreshold.value);
  cfg.alerts.cooldown_seconds = Number(form.cooldown.value);
  cfg.up_filter.move_pct = Number(form.upMove.value);
  cfg.up_filter.exit_move_pct = Math.min(cfg.up_filter.exit_move_pct, cfg.up_filter.move_pct);
  cfg.down_filter.move_pct = Number(form.downMove.value);
  cfg.down_filter.exit_move_pct = Math.min(cfg.down_filter.exit_move_pct, cfg.down_filter.move_pct);
  cfg.calculation.min_today_volume = Number(form.minVolume.value);
  cfg.calculation.lookback_seconds = Number(form.lookback.value);
  cfg.calculation.max_stale_seconds = Number(form.stale.value);
  cfg.alerts.up.sound_file = form.upSound.value.trim();
  cfg.alerts.down.sound_file = form.downSound.value.trim();
  cfg.alerts.volume = Number(form.volume.value);
  cfg.alerts.max_simultaneous_sounds = Number(form.maxSounds.value);
  cfg.alerts.on_sound_limit = form.soundPolicy.value;
  cfg.alerts.master_mute = form.masterMute.checked;
  cfg.alerts.up.mute = form.upMute.checked;
  cfg.alerts.down.mute = form.downMute.checked;
  return cfg;
}

async function saveConfig() {
  $("saveState").textContent = "saving";
  const cfg = await fetchJSON("/api/config", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(formToConfig())
  });
  setConfigToForm(cfg);
  $("saveState").textContent = "saved";
  setTimeout(() => $("saveState").textContent = "", 1200);
}

function renderSnapshot(s) {
  $("upCount").textContent = fmt(s.up_count);
  $("downCount").textContent = fmt(s.down_count);
  $("delta").textContent = fmt(s.delta);
  $("biasScore").textContent = fmt(s.bias_score, 1);
  colorBias(s.delta);
  highlightWinner(s.up_count, s.down_count);
  $("tracked").textContent = fmt(s.tracked);
  $("warming").textContent = fmt(s.warming);
  $("stale").textContent = fmt(s.stale);
  $("lastUpdate").textContent = s.last_update || "--";
  updateClockFromSnapshot(s);
  $("conn").textContent = s.connection_status || "unknown";
  $("mode").textContent = s.data_mode || "second_aggregates";
  $("active").textContent = s.active_session ? "active session" : "inactive session";
  $("active").style.borderColor = s.active_session ? "var(--up)" : "var(--line)";
  $("sessionNote").textContent = "Today: " + (s.session_window || "04:00:00 to 20:00:00 New York time");
  renderRanks("topUp", s.top_up || [], true);
  renderRanks("topDown", s.top_down || [], false);
  if (s.config) currentConfig = s.config;
}

function colorBias(delta) {
  const card = document.querySelector(".signal.delta");
  card.classList.remove("bias-up", "bias-down", "bias-neutral");
  if (delta > 0) {
    card.classList.add("bias-up");
  } else if (delta < 0) {
    card.classList.add("bias-down");
  } else {
    card.classList.add("bias-neutral");
  }
}

function updateClockFromSnapshot(s) {
  if ((s.connection_status || "").startsWith("replay:") && s.last_update) {
    const match = s.last_update.match(/\b(\d{2}:\d{2}:\d{2})\b/);
    if (match) {
      replayClockText = match[1];
      replayClockUntil = Date.now() + 2500;
      $("clock").textContent = replayClockText;
      return;
    }
  }
  replayClockUntil = 0;
}

function tickClock() {
  if (Date.now() < replayClockUntil && replayClockText) {
    $("clock").textContent = replayClockText;
    return;
  }
  $("clock").textContent = new Intl.DateTimeFormat("en-US", {
    timeZone: "America/New_York",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false
  }).format(new Date());
}

function highlightWinner(upCount, downCount) {
  const up = $("upCard");
  const down = $("downCard");
  up.classList.remove("winner", "loser");
  down.classList.remove("winner", "loser");
  if (upCount > downCount) {
    up.classList.add("winner");
    down.classList.add("loser");
  } else if (downCount > upCount) {
    down.classList.add("winner");
    up.classList.add("loser");
  }
}

function renderRanks(id, rows, up) {
  const el = $(id);
  el.innerHTML = "";
  for (const r of rows) {
    const li = document.createElement("li");
    li.innerHTML = `<span class="sym">${r.symbol}</span><span class="vol">${fmt(r.today_volume)} vol</span><span class="pct">${up ? "+" : ""}${fmt(r.change_pct, 2)}%</span>`;
    el.appendChild(li);
  }
}

async function ensureAudio() {
  if (!audioCtx) audioCtx = new (window.AudioContext || window.webkitAudioContext)();
  if (audioCtx.state === "suspended") await audioCtx.resume();
}

async function decodeSound(path) {
  if (!path || buffers.has(path)) return buffers.get(path);
  await ensureAudio();
  const res = await fetch("/sound?file=" + encodeURIComponent(path));
  if (!res.ok) throw new Error("missing sound: " + path);
  const arr = await res.arrayBuffer();
  const buf = await audioCtx.decodeAudioData(arr);
  buffers.set(path, buf);
  return buf;
}

async function preloadSounds(cfg) {
  if (!audioCtx) return;
  for (const path of [cfg.alerts.up.sound_file, cfg.alerts.down.sound_file, cfg.alerts.both_sound_file]) {
    decodeSound(path).catch(() => {});
  }
}

async function playSound(path, volume, opts = {}) {
  if (!currentConfig) return;
  if (!opts.force && (!armed || currentConfig.alerts.master_mute)) return;
  const myGeneration = opts.priority ? ++playbackGeneration : playbackGeneration;
  await ensureAudio();
  if (myGeneration !== playbackGeneration) return;
  if (opts.priority) {
    for (const source of activeSounds) {
      try { source.stop(); } catch (_) {}
    }
    activeSounds = [];
  }
  const max = currentConfig.alerts.max_simultaneous_sounds || 4;
  if (!opts.priority && activeSounds.length >= max) {
    if (currentConfig.alerts.on_sound_limit === "stop_oldest") {
      const oldest = activeSounds.shift();
      try { oldest.stop(); } catch (_) {}
    } else {
      return;
    }
  }
  const buffer = await decodeSound(path);
  if (myGeneration !== playbackGeneration) return;
  const source = audioCtx.createBufferSource();
  const gain = audioCtx.createGain();
  gain.gain.value = Math.max(0, Math.min(1, volume ?? currentConfig.alerts.volume ?? 0.8));
  source.buffer = buffer;
  source.connect(gain).connect(audioCtx.destination);
  activeSounds.push(source);
  source.onended = () => activeSounds = activeSounds.filter(s => s !== source);
  source.start();
}

function handleAlert(ev) {
  if (!currentConfig) return;
  if (ev.side === "both") {
    playSound(ev.sound_file, ev.volume, { priority: true }).catch(err => $("saveState").textContent = err.message);
    return;
  }
  if (ev.side === "up" && currentConfig.alerts.up.mute) return;
  if (ev.side === "down" && currentConfig.alerts.down.mute) return;
  playSound(ev.sound_file, ev.volume).catch(err => $("saveState").textContent = err.message);
}

async function boot() {
  const cfg = await fetchJSON("/api/config");
  setConfigToForm(cfg);
  const status = await fetchJSON("/api/status");
  renderSnapshot(status);

  const events = new EventSource("/events");
  events.onmessage = (msg) => {
    const ev = JSON.parse(msg.data);
    if (ev.type === "snapshot") renderSnapshot(ev.snapshot);
    if (ev.type === "alert") handleAlert(ev);
  };
  events.onerror = () => $("conn").textContent = "event stream reconnecting";
}

form.addEventListener("submit", (e) => {
  e.preventDefault();
  saveConfig().catch(err => $("saveState").textContent = err.message);
});

form.addEventListener("change", () => {
  saveConfig().catch(err => $("saveState").textContent = err.message);
});

$("armBtn").addEventListener("click", async () => {
  armed = !armed;
  if (armed) await ensureAudio();
  $("armBtn").textContent = armed ? "Alerts Armed" : "Arm Alerts";
});

$("muteBtn").addEventListener("click", async () => {
  form.masterMute.checked = !form.masterMute.checked;
  await saveConfig();
});

$("testUp").addEventListener("click", () => playSound(form.upSound.value, Number(form.volume.value), { force: true }));
$("testDown").addEventListener("click", () => playSound(form.downSound.value, Number(form.volume.value), { force: true }));

boot().catch(err => $("conn").textContent = err.message);
tickClock();
setInterval(tickClock, 1000);
