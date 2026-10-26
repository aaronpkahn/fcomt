// Minimal uPlot dashboard fed by /v1/dashboard (SQLite-backed). Refreshes every 30s.
(function () {
  const COLORS = { gateway: "#8e44ad", "google-dns": "#2980b9", "cloudflare-dns": "#e67e22", "aws-us-east-1": "#27ae60" };
  const fallback = ["#c0392b", "#16a085", "#d35400", "#2c3e50"];
  const WINDOWS = ["1h", "6h", "24h", "7d"];
  const KEY = "fcomt.dashWindow";
  function currentWindow() {
    const m = location.hash.match(/window=([^&]+)/);
    if (m && WINDOWS.includes(m[1])) return m[1];
    const s = localStorage.getItem(KEY);
    return WINDOWS.includes(s) ? s : "1h";
  }
  let win = currentWindow();
  let plot = null;
  let plotKey = "";
  const fmt = (iso) => new Date(iso).toLocaleString("en-US", { timeZone: "America/Los_Angeles", hour12: false });
  const dur = (ms) => {
    const s = Math.round(ms / 1000);
    return s < 120 ? `${s}s` : s < 7200 ? `${Math.round(s / 60)}m` : `${(s / 3600).toFixed(1)}h`;
  };

  function renderChart(d) {
    const el = document.getElementById("latency-chart");
    const names = d.targets.map((t) => t.target);
    const data = [d.times_ms.map((t) => t / 1000), ...d.latency];
    const key = names.join(",");
    if (plot && key === plotKey) {
      plot.setData(data);
      return;
    }
    if (plot) plot.destroy();
    el.innerHTML = "";
    plotKey = key;
    const opts = {
      width: Math.max(el.clientWidth, 600),
      height: 300,
      scales: { x: { time: true } },
      axes: [{}, { label: "ms" }],
      series: [{}].concat(
        names.map((n, i) => ({ label: n, stroke: COLORS[n] || fallback[i % fallback.length], width: 1.5, spanGaps: false }))
      ),
    };
    plot = new uPlot(opts, data, el);
  }

  function renderUptime(d) {
    const el = document.getElementById("uptime-bars");
    el.innerHTML = "";
    d.targets.forEach((t, i) => {
      const color = t.uptime_pct >= 99.9 ? "#27ae60" : t.uptime_pct >= 99 ? "#f39c12" : "#c0392b";
      const row = document.createElement("div");
      row.className = "bar-row";
      row.innerHTML = `<span class="bar-label">${t.target}</span><div class="bar-track"><div class="bar-fill" style="width:${t.uptime_pct}%;background:${color}"></div></div><span class="bar-val">${t.uptime_pct.toFixed(2)}% (${t.success_count}/${t.count})</span>`;
      el.appendChild(row);
    });
  }

  function renderEvents(d) {
    document.getElementById("outage-count").textContent = d.outages.length;
    document.getElementById("gap-count").textContent = d.gaps.length;
    document.getElementById("incident-count").textContent = d.incidents.length;
    const items = [];
    d.outages.forEach((o) =>
      items.push({ t: o.start, cls: "outage", text: `OUTAGE ${fmt(o.start)} → ${o.end ? fmt(o.end) : "ongoing"} (${dur((o.end ? new Date(o.end) : new Date()) - new Date(o.start))}) cause=${o.cause}` })
    );
    d.gaps.forEach((g) => items.push({ t: g.start, cls: "gap", text: `GAP    ${fmt(g.start)} → ${fmt(g.end)} (${dur(new Date(g.end) - new Date(g.start))}) ${g.reason}` }));
    d.incidents.forEach((e) => items.push({ t: e.time, cls: "incident", text: `${e.state.toUpperCase()} ${fmt(e.time)} ${e.target}: ${e.message}` }));
    items.sort((a, b) => new Date(b.t) - new Date(a.t));
    const ul = document.getElementById("event-list");
    ul.innerHTML = items.length ? "" : "<li>None recorded yet.</li>";
    items.slice(0, 200).forEach((it) => {
      const li = document.createElement("li");
      li.className = it.cls;
      li.textContent = it.text;
      ul.appendChild(li);
    });
  }

  async function refresh() {
    try {
      const res = await fetch("/v1/dashboard?window=" + encodeURIComponent(win));
      const d = await res.json();
      if (!res.ok) throw new Error(d.error || res.status);
      if (typeof uPlot !== "undefined") renderChart(d);
      else document.getElementById("latency-chart").textContent = "uPlot failed to load from CDN";
      renderUptime(d);
      renderEvents(d);
      document.getElementById("dash-updated").textContent = `${d.points} pts / ${d.raw_rounds} rounds · updated ` + new Date().toLocaleTimeString("en-US", { timeZone: "America/Los_Angeles" }) + " PT";
    } catch (err) {
      document.getElementById("dash-updated").textContent = "refresh failed: " + err.message;
    }
  }

  fetch("/v1/config").then((r) => r.json()).then((c) => {
    if (!(c.api && c.api.enable_demo)) {
      const p = document.getElementById("seeder-panel");
      if (p) p.style.display = "none";
    }
  }).catch(() => {});

  const sel = document.getElementById("dash-window");
  const setLabels = () => document.querySelectorAll(".win-label").forEach((e) => (e.textContent = "last " + win));
  sel.value = win;
  setLabels();
  sel.addEventListener("change", () => {
    win = sel.value;
    localStorage.setItem(KEY, win);
    history.replaceState(null, "", "#window=" + win);
    setLabels();
    refresh();
  });
  window.addEventListener("hashchange", () => {
    const w = currentWindow();
    if (w !== win) { win = w; sel.value = w; setLabels(); refresh(); }
  });

  refresh();
  setInterval(refresh, 30000);
})();
