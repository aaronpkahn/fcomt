const scenarioSelect = document.getElementById("scenario-select");
const seedBtn = document.getElementById("seed-btn");
const seedStatus = document.getElementById("seed-status");
const summaryBody = document.getElementById("summary-body");
const incidentList = document.getElementById("incident-list");
const profileValue = document.getElementById("profile-value");
const targetsValue = document.getElementById("targets-value");
const incidentsValue = document.getElementById("incidents-value");
const timeseriesTarget = document.getElementById("timeseries-target");
const timeseriesWindow = document.getElementById("timeseries-window");
const timeseriesCanvas = document.getElementById("timeseries-canvas");
let lastTargetNames = [];
let failureDotHits = [];

const chartTooltip = document.createElement("div");
chartTooltip.className = "chart-tooltip";
chartTooltip.style.display = "none";
document.body.appendChild(chartTooltip);

async function getJSON(url, options) {
  const res = await fetch(url, options);
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`${res.status}: ${text}`);
  }
  return res.json();
}

function fmtMs(value) {
  return `${Number(value || 0).toFixed(2)} ms`;
}

function fmtPct(value) {
  return `${Number(value || 0).toFixed(2)}%`;
}

async function loadScenarios() {
  const scenarios = await getJSON("/v1/examples");
  scenarioSelect.innerHTML = "";
  for (const s of scenarios) {
    const opt = document.createElement("option");
    opt.value = s.name;
    opt.textContent = `${s.title} (${s.profile})`;
    scenarioSelect.appendChild(opt);
  }
}

async function seedScenario() {
  if (!scenarioSelect.value) {
    return;
  }
  seedStatus.textContent = "Loading...";
  try {
    const result = await getJSON(`/v1/examples/seed?name=${encodeURIComponent(scenarioSelect.value)}`, {
      method: "POST"
    });
    seedStatus.textContent = `Loaded ${result.seeded.title}`;
    await refresh();
  } catch (err) {
    seedStatus.textContent = err.message;
  }
}

function renderSummaries(summaries) {
  summaryBody.innerHTML = "";
  for (const s of summaries) {
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>${s.target}</td>
      <td>${s.count}</td>
      <td>${fmtPct(s.availability_pct)}</td>
      <td>${fmtPct(s.loss_pct)}</td>
      <td>${fmtMs(s.avg_latency_ms)}</td>
      <td>${fmtMs(s.p95_latency_ms)}</td>
      <td>${fmtMs(s.jitter_ms)}</td>
    `;
    summaryBody.appendChild(tr);
  }
}

function renderIncidents(items) {
  incidentList.innerHTML = "";
  if (items.length === 0) {
    const li = document.createElement("li");
    li.textContent = "No incidents.";
    incidentList.appendChild(li);
    return;
  }
  for (const e of items) {
    const li = document.createElement("li");
    li.className = e.severity === "critical" ? "severity-critical" : "";
    li.textContent = `[${e.state}] ${e.target}: ${e.message}`;
    incidentList.appendChild(li);
  }
}

function updateTargetPicker(summaries) {
  const targets = summaries.map((s) => s.target).filter(Boolean);
  const current = timeseriesTarget.value;
  if (targets.length === 0) {
    timeseriesTarget.innerHTML = "";
    lastTargetNames = [];
    return "";
  }

  const changed = targets.join("|") !== lastTargetNames.join("|");
  if (changed) {
    timeseriesTarget.innerHTML = "";
    for (const target of targets) {
      const opt = document.createElement("option");
      opt.value = target;
      opt.textContent = target;
      timeseriesTarget.appendChild(opt);
    }
    lastTargetNames = targets;
  }

  if (targets.includes(current)) {
    timeseriesTarget.value = current;
    return current;
  }
  timeseriesTarget.value = targets[0];
  return targets[0];
}

function drawTimeSeries(points, target, windowLabel) {
  const ctx = timeseriesCanvas.getContext("2d");
  const rect = timeseriesCanvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  timeseriesCanvas.width = Math.max(300, Math.floor(rect.width * dpr));
  timeseriesCanvas.height = Math.floor(260 * dpr);
  ctx.scale(dpr, dpr);

  const width = timeseriesCanvas.width / dpr;
  const height = timeseriesCanvas.height / dpr;
  ctx.clearRect(0, 0, width, height);

  const pad = { left: 48, right: 16, top: 18, bottom: 30 };
  const plotW = width - pad.left - pad.right;
  const plotH = height - pad.top - pad.bottom;
  failureDotHits = [];

  ctx.fillStyle = "#111827";
  ctx.font = "12px IBM Plex Sans, sans-serif";
  if (!points || points.length === 0) {
    ctx.fillText(`No samples for ${target || "selected target"} (${windowLabel}).`, pad.left, pad.top + 20);
    return;
  }

  const sorted = [...points].sort((a, b) => Date.parse(a.timestamp) - Date.parse(b.timestamp));
  const minTs = Date.parse(sorted[0].timestamp);
  const maxTs = Date.parse(sorted[sorted.length - 1].timestamp);
  const tsRange = Math.max(1, maxTs - minTs);
  const successPoints = sorted.filter((p) => p.success);
  const maxLatency = Math.max(20, ...successPoints.map((p) => p.latency_ms || 0));

  ctx.strokeStyle = "#d1d5db";
  ctx.lineWidth = 1;
  for (let i = 0; i <= 4; i++) {
    const y = pad.top + (plotH * i) / 4;
    ctx.beginPath();
    ctx.moveTo(pad.left, y);
    ctx.lineTo(width - pad.right, y);
    ctx.stroke();
  }

  ctx.fillStyle = "#6b7280";
  ctx.font = "11px IBM Plex Sans, sans-serif";
  for (let i = 0; i <= 4; i++) {
    const value = maxLatency - (maxLatency * i) / 4;
    const y = pad.top + (plotH * i) / 4 + 4;
    ctx.fillText(`${value.toFixed(0)}ms`, 6, y);
  }

  ctx.strokeStyle = "#0f766e";
  ctx.lineWidth = 2;
  ctx.beginPath();
  let started = false;
  for (const p of sorted) {
    if (!p.success) continue;
    const x = pad.left + ((Date.parse(p.timestamp) - minTs) / tsRange) * plotW;
    const y = pad.top + plotH - ((p.latency_ms || 0) / maxLatency) * plotH;
    if (!started) {
      ctx.moveTo(x, y);
      started = true;
    } else {
      ctx.lineTo(x, y);
    }
  }
  ctx.stroke();

  ctx.fillStyle = "#b91c1c";
  for (const p of sorted) {
    if (p.success) continue;
    const x = pad.left + ((Date.parse(p.timestamp) - minTs) / tsRange) * plotW;
    const y = pad.top + plotH;
    ctx.beginPath();
    ctx.arc(x, y, 3, 0, Math.PI * 2);
    ctx.fill();
    failureDotHits.push({ x, y, point: p });
  }

  ctx.fillStyle = "#6b7280";
  ctx.font = "11px IBM Plex Sans, sans-serif";
  const startLabel = new Date(minTs).toLocaleString();
  const endLabel = new Date(maxTs).toLocaleString();
  ctx.fillText(startLabel, pad.left, height - 8);
  const endWidth = ctx.measureText(endLabel).width;
  ctx.fillText(endLabel, width - pad.right - endWidth, height - 8);
}

function hideChartTooltip() {
  chartTooltip.style.display = "none";
}

function showChartTooltip(hit, event) {
  const p = hit.point;
  const when = new Date(p.timestamp).toLocaleString();
  const error = p.error || "probe failed";
  chartTooltip.innerHTML = `<strong>Failure</strong><br>${when}<br>Error: ${error}`;
  chartTooltip.style.display = "block";
  chartTooltip.style.left = `${event.pageX + 12}px`;
  chartTooltip.style.top = `${event.pageY - 10}px`;
}

function handleChartHover(event) {
  if (!failureDotHits.length) {
    hideChartTooltip();
    return;
  }
  const rect = timeseriesCanvas.getBoundingClientRect();
  const x = event.clientX - rect.left;
  const y = event.clientY - rect.top;

  let nearest = null;
  let bestDist = Infinity;
  for (const dot of failureDotHits) {
    const dx = x - dot.x;
    const dy = y - dot.y;
    const dist = Math.sqrt(dx * dx + dy * dy);
    if (dist < bestDist) {
      bestDist = dist;
      nearest = dot;
    }
  }

  if (!nearest || bestDist > 10) {
    hideChartTooltip();
    return;
  }
  showChartTooltip(nearest, event);
}

async function refresh() {
  const [cfg, summaries, incidents] = await Promise.all([
    getJSON("/v1/config"),
    getJSON("/v1/metrics/summary?window=24h"),
    getJSON("/v1/incidents")
  ]);

  const monitoring = cfg.monitoring || cfg.Monitoring || {};
  const profile = monitoring.profile || monitoring.Profile || "-";
  const destinations = monitoring.destinations || monitoring.Destinations || [];

  profileValue.textContent = profile;
  targetsValue.textContent = `${destinations.length}`;
  incidentsValue.textContent = `${incidents.length}`;
  const summaryList = Array.isArray(summaries) ? summaries : [];
  renderSummaries(summaryList);
  renderIncidents(incidents);

  const target = updateTargetPicker(summaryList);
  if (!target) {
    drawTimeSeries([], "", timeseriesWindow.value);
    return;
  }

  const ts = await getJSON(`/v1/metrics/timeseries?target=${encodeURIComponent(target)}&window=${encodeURIComponent(timeseriesWindow.value)}`);
  const series = (ts.series || [])[0];
  drawTimeSeries(series ? series.points : [], target, timeseriesWindow.value);
}

seedBtn.addEventListener("click", seedScenario);
timeseriesTarget.addEventListener("change", refresh);
timeseriesWindow.addEventListener("change", refresh);
window.addEventListener("resize", refresh);
timeseriesCanvas.addEventListener("mousemove", handleChartHover);
timeseriesCanvas.addEventListener("mouseleave", hideChartTooltip);

async function init() {
  try {
    await loadScenarios();
    await refresh();
    setInterval(refresh, 5000);
  } catch (err) {
    seedStatus.textContent = err.message;
  }
}

init();
