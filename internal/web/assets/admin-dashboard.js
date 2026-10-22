"use strict";
let dashboardPaused = false,
  dashboardLoading = false,
  historyPage = 1,
  historyLoading = 0;
const dashboardSnapshots = new Map();
function changedSnapshot(key, data, focusedContainer) {
  if (focusedContainer?.contains(document.activeElement)) return false;
  const next = JSON.stringify(data, (key, value) =>
    key === "now" || key === "generatedAt" ? undefined : value,
  );
  if (dashboardSnapshots.get(key) === next) return false;
  dashboardSnapshots.set(key, next);
  return true;
}
let historyFilters = {
  q: "",
  status: "",
  provider: "",
  mode: "",
  sort: "newest",
};
const formatNumber = (n) => new Intl.NumberFormat().format(n || 0);
const duration = (ms) =>
  ms == null
    ? "—"
    : ms < 1000
      ? Math.round(ms) + " ms"
      : ms < 60000
        ? (ms / 1000).toFixed(1) + "s"
        : Math.floor(ms / 60000) + "m " + Math.round((ms % 60000) / 1000) + "s";
const percent = (n) => (n == null ? "—" : n.toFixed(1) + "%");
const cabinName = (v) =>
  ({
    economy: "Economy",
    premium_economy: "Premium economy",
    business: "Business",
    first: "First",
  })[v] || v;
function metric(value, label, note, accent = "") {
  const d = el("article", undefined, "metric " + accent);
  d.append(
    el("span", label, "metric-label"),
    el("strong", value),
    el("small", note),
  );
  return d;
}
function rangeQuery() {
  return new URLSearchParams({ range: $("analytics-range").value });
}
function historyQuery() {
  const q = rangeQuery();
  for (const [k, v] of Object.entries(historyFilters)) if (v) q.set(k, v);
  q.set("page", historyPage);
  q.set("limit", $("history-limit").value);
  return q;
}
function clearDashboard() {
  dashboardSnapshots.clear();
  historyPage = 1;
  historyLoading++;
  for (const id of [
    "summary",
    "recent-jobs",
    "active-searches",
    "route-stats",
    "failure-stats",
    "trend-chart",
    "detail-body",
    "fleet-health",
    "performance-strip",
  ])
    $(id).replaceChildren();
  if ($("search-detail").open) $("search-detail").close();
}
const viewLabels = {
  overview: [
    "Overview",
    "A clear picture of searches, reliability and session health.",
  ],
  searches: [
    "Search history",
    "The full ledger. Filter, inspect and export your all-time search history.",
  ],
  accounts: [
    "Accounts & sessions",
    "Keep the fleet healthy, authenticated and ready for the next search.",
  ],
  flights: [
    "Saved flights",
    "Recent complete results, ready to reuse without another live request.",
  ],
  proxies: [
    "Sticky proxies",
    "Manage the connection pool behind your sessions.",
  ],
  settings: [
    "Settings & API keys",
    "One place for provider configuration and workspace preferences.",
  ],
};
function currentView() {
  const key = location.hash.slice(1);
  return viewLabels[key] ? key : "overview";
}
function setView() {
  const key = currentView();
  for (const panel of document.querySelectorAll("[data-view]"))
    panel.hidden = panel.dataset.view !== key;
  for (const a of document.querySelectorAll("[data-nav]")) {
    const active = a.dataset.nav === key;
    a.classList.toggle("selected", active);
    if (active) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  }
  $("view-title").textContent = viewLabels[key][0];
  $("view-subtitle").textContent = viewLabels[key][1];
}
function switchView() {
  setView();
  if (polling && currentView() === "searches")
    loadHistory().catch((e) => message(e.message));
}

function renderHealth(data) {
	const accountIds = new Set(data.sessions.map((s) => s.id));
  const ready = data.sessions.filter(
    (s) =>
      s.session.ready &&
      !s.busy &&
      !s.needsAttention &&
      s.state !== "disabled" &&
      (!s.nextAttempt || new Date(s.nextAttempt) <= new Date()),
  ).length;
  const busy = data.sessions.filter((s) => s.busy).length,
    attention = new Set([
      ...data.sessions
        .filter((s) => s.needsAttention)
        .map((s) => s.id),
      ...(data.accountSetups || [])
        .filter((s) => ["failed", "attention"].includes(s.status) &&
          (!s.accountId || !accountIds.has(s.accountId)))
        .map((s) => s.accountId || s.id),
    ]).size;
  const retrying = data.sessions.filter(
    (s) => !s.needsAttention && !s.busy && s.nextAttempt &&
      new Date(s.nextAttempt) > new Date(),
  ).length;
  const card = $("fleet-health");
  card.replaceChildren();
  const hero = el("div", undefined, "fleet-number");
  hero.append(
    el("strong", String(ready)),
    el(
      "span",
      "ready / " + data.accounts.filter((a) => a.enabled).length + " enabled",
    ),
  );
  card.append(hero);
  const meter = el("div", undefined, "fleet-meter");
  for (const s of data.sessions) {
    const dot = el(
      "span",
      undefined,
      s.busy
        ? "busy"
        : s.needsAttention
          ? "issue"
          : s.session.ready
            ? "available"
            : "idle",
    );
    dot.title =
      s.state +
      (s.session.expiresAt ? " · expires " + date(s.session.expiresAt) : "");
    meter.append(dot);
  }
  if (!data.sessions.length) meter.append(el("span", undefined, "idle"));
  card.append(meter);
  for (const [n, label] of [
    [busy, "accounts working"],
    [attention, "need attention"],
    [retrying, "automatic retry scheduled"],
    [data.proxyCount, "proxy endpoints"],
  ]) {
    const row = el("div", undefined, "health-row");
    row.append(el("span", label), el("strong", String(n)));
    card.append(row);
  }
  $("retention-note").textContent =
    "All-time search history · " + (data.concurrency || 4) + " workers";
  const accountNav = document.querySelector('[data-nav="accounts"]');
  accountNav.title =
    ready + " ready, " + busy + " busy, " + attention + " need attention";
}
function renderAnalytics(a) {
  $("history-badge").textContent = formatNumber(a.total);
  $("summary").replaceChildren(
    metric(formatNumber(a.total), "SEARCHES", "Unique jobs in selected window"),
    metric(
      percent(a.successRate),
      "SUCCESS RATE",
      a.completed + " completed · " + a.failed + " failed",
      "positive",
    ),
    metric(
      percent(a.liveSuccessRate),
      "LIVE RELIABILITY",
      a.liveCompleted + " completed · " + a.liveFailed + " failed live",
    ),
    metric(
      duration(a.medianLiveMs),
      "MEDIAN LIVE TIME",
      "Successful live jobs, including queue",
    ),
  );
  $("performance-strip").replaceChildren(
    ...[
      [
        formatNumber(a.cacheHits),
        "cache hits",
        percent(a.total ? (100 * a.cacheHits) / a.total : null) +
          " of searches",
      ],
      [
        formatNumber(a.recovered),
        "recovered successes",
        a.retryEvents + " retry events across all jobs",
      ],
      [
        duration(a.p95LiveMs),
        "P95 live duration",
        "95% of successful live jobs finish within this",
      ],
      [
        formatNumber(a.cancelled + a.interrupted),
        "stopped searches",
        a.cancelled + " cancelled · " + a.interrupted + " interrupted",
      ],
    ].map(([v, l, n]) => {
      const d = el("div");
      d.append(el("strong", v), el("span", l), el("small", n));
      return d;
    }),
  );
  $("trend-total").textContent = formatNumber(a.total) + " searches";
  const max = Math.max(
      1,
      ...a.trend.map((b) => b.success + b.failed + b.cached + b.other),
    ),
    chart = $("trend-chart");
  chart.replaceChildren();
  for (const b of a.trend) {
    const bar = el("div", undefined, "chart-bar");
    bar.title =
      date(b.at) +
      "\n" +
      b.success +
      " live successes · " +
      b.failed +
      " failed · " +
      b.cached +
      " cached · " +
      b.other +
      " other";
    const stack = el("div", undefined, "chart-stack");
    stack.style.height =
      Math.max(2, (100 * (b.success + b.failed + b.cached + b.other)) / max) +
      "%";
    for (const [k, cls] of [
      ["success", "segment-success"],
      ["cached", "segment-cached"],
      ["failed", "segment-failed"],
      ["other", "segment-other"],
    ]) {
      if (b[k]) {
        const segment = el("span", undefined, cls);
        segment.style.flex = String(b[k]);
        stack.append(segment);
      }
    }
    if (!stack.children.length) stack.classList.add("zero");
    bar.append(stack);
    chart.append(bar);
  }
  chart.setAttribute(
    "aria-label",
    a.total +
      " searches in " +
      a.trend.length +
      " time buckets; " +
      a.completed +
      " completed and " +
      a.failed +
      " failed.",
  );
  const labels = $("trend-labels");
  labels.replaceChildren();
  for (const b of [a.trend[0], a.trend[12], a.trend[23]]) {
    const at = new Date(b.at);
    labels.append(
      el(
        "span",
        $("analytics-range").value === "1h" ||
          $("analytics-range").value === "24h"
          ? at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
          : at.toLocaleDateString([], { month: "short", day: "numeric" }),
      ),
    );
  }
  const routes = $("route-stats");
  routes.replaceChildren();
  if (!a.routes.length)
    routes.append(
      empty("No routes yet", "Your most searched routes will appear here."),
    );
  for (const r of a.routes) {
    const row = el("button", undefined, "route-stat");
    row.type = "button";
    const label = cell(
      r.route,
      r.completed + " completed · " + r.failed + " failed",
    );
    const count = cell(
      formatNumber(r.searches),
      r.averageMs
        ? duration(r.averageMs) + " avg live"
        : "No completed live timing",
    );
    row.append(label, count);
    row.onclick = () => {
      $("history-filters").reset();
      historyFilters = {
        q: r.route.replace(" → ", " "),
        status: "",
        provider: "",
        mode: "",
        sort: "newest",
      };
      $("history-query").value = historyFilters.q;
      historyPage = 1;
      location.hash = "searches";
      loadHistory().catch((e) => message(e.message));
    };
    routes.append(row);
  }
  const errors = $("failure-stats");
  errors.replaceChildren();
  if (!a.failures.length)
    errors.append(
      empty(
        "No failed searches in this window",
        "Successful recoveries remain successes.",
      ),
    );
  for (const f of a.failures) {
    const row = el("div", undefined, "failure-stat");
    row.append(
      cell(f.code.replaceAll("_", " "), f.stage || "Stage not recorded"),
      el("strong", String(f.count)),
    );
    errors.append(row);
  }
  $("active-count").textContent = a.running + a.queued + " active";
  const active = $("active-searches");
  active.replaceChildren();
  if (!a.active.length)
    active.append(
      empty(
        "All clear. Nothing in the queue.",
        "Prepared sessions are standing by for the next search.",
      ),
    );
  for (const j of a.active) {
    const card = el("article", undefined, "active-job"),
      top = el("div", undefined, "panel-top");
    top.append(
      cell(
        j.query.origin + " → " + j.query.destination,
        j.query.date + " · " + cabinName(j.query.cabin),
      ),
      el("span", j.status, "pill " + j.status),
    );
    const progress = el("div", undefined, "job-progress"),
      fill = el("span");
    if (j.progress?.total) {
      fill.style.width =
        Math.max(
          3,
          Math.min(100, (100 * j.progress.completed) / j.progress.total),
        ) + "%";
    } else fill.className = "indeterminate";
    progress.append(fill);
    const bottom = el("div", undefined, "panel-top");
    bottom.append(
      cell(j.progress?.stage || "Waiting", j.progress?.message),
      action("Inspect", () => openSearch(j.id)),
    );
    card.append(top, progress, bottom);
    active.append(card);
  }
}
function empty(title, note) {
  const d = el("div", undefined, "empty-state");
  d.append(el("strong", title), el("p", note));
  return d;
}
function searchTable(items, compact = false) {
  return table(
    compact
      ? ["Route / departure", "Outcome", "Source", "Duration", "Details"]
      : [
          "Route / departure",
          "Provider / cabin",
          "Outcome",
          "Source",
          "Duration",
          "Flights",
          "Started",
          "Details",
        ],
    items.map((j) => {
      const outcome = el("div");
      outcome.append(el("span", j.status, "pill " + j.status));
      if (j.recoveryCount)
        outcome.append(
          el(
            "small",
            j.recoveryCount +
              " automatic recover" +
              (j.recoveryCount === 1 ? "y" : "ies"),
          ),
        );
      if (j.error) outcome.append(el("small", j.error.code));
      const controls = el("div", undefined, "row-actions");
      controls.append(action("Inspect", () => openSearch(j.id)));
      if (["running", "queued"].includes(j.status))
        controls.append(
          action("Cancel", async () => {
            await api("/api/searches/" + j.id, { method: "DELETE" });
            await refresh();
          }),
        );
      const route = cell(
        j.query.origin + " → " + j.query.destination,
        j.query.date,
      );
      const source = el(
        "span",
        j.cached ? "Cached" : "Live",
        "source-badge " + (j.cached ? "cached" : "live"),
      );
      return compact
        ? [route, outcome, source, duration(j.durationMs), controls]
        : [
            route,
            cell(j.query.provider.toUpperCase(), cabinName(j.query.cabin)),
            outcome,
            source,
            cell(
              duration(j.durationMs),
              ["running", "queued"].includes(j.status)
                ? "elapsed"
                : "wall time",
            ),
            formatNumber(j.flights),
            cell(date(j.createdAt), j.id.slice(0, 8)),
            controls,
          ];
    }),
  );
}
async function loadHistory() {
  const version = ++historyLoading;
  const result = await api("/api/admin/history?" + historyQuery());
  if (version !== historyLoading || !polling) return;
  const pages = Math.max(1, Math.ceil(result.total / result.limit));
  if (historyPage > pages) {
    historyPage = pages;
    return loadHistory();
  }
  if (changedSnapshot("history", result.items, $("job-list")))
    $("job-list").replaceChildren(searchTable(result.items));
  const start = result.total ? (result.page - 1) * result.limit + 1 : 0;
  $("history-count").textContent =
    start +
    "–" +
    Math.min(result.page * result.limit, result.total) +
    " of " +
    formatNumber(result.total) +
    " searches";
  renderPagination($("history-pages"), result.page, pages, async (page) => {
    historyPage = page;
    await loadHistory().catch((e) => message(e.message));
  });
}
async function refreshDashboard() {
  if (dashboardLoading) return false;
  dashboardLoading = true;
  try {
    const q = rangeQuery();
    const [data, stats, recent] = await Promise.all([
      api("/api/admin/overview"),
      api("/api/admin/analytics?" + q),
      api("/api/admin/history?" + q + "&limit=6"),
    ]);
    if (!polling) return false;
    if (
      !$("flight-list").contains(document.activeElement) &&
      changedSnapshot("overview", data)
    )
      render(data);
    if (changedSnapshot("analytics", stats, $("overview")))
      renderAnalytics(stats);
    if (changedSnapshot("recent", recent.items, $("recent-jobs")))
      $("recent-jobs").replaceChildren(searchTable(recent.items, true));
    if (currentView() === "searches") await loadHistory();
    $("last-sync").textContent =
      "Updated " +
      new Date().toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      });
    setView();
  } finally {
    dashboardLoading = false;
  }
}

async function openSearch(id) {
  const j = await api("/api/admin/search?id=" + encodeURIComponent(id));
  if (!polling) return;
  const body = $("detail-body");
  body.replaceChildren();
  $("detail-title").textContent = j.query.origin + " → " + j.query.destination;
  const badges = el("div", undefined, "detail-badges");
  badges.append(
    el("span", j.status, "pill " + j.status),
    el(
      "span",
      j.result?.cached ? "Cached result" : "Live search",
      "source-badge",
    ),
  );
  body.append(badges);
  const facts = el("dl", undefined, "detail-facts");
  for (const [k, v] of [
    ["Search ID", j.id],
    ["Departure", j.query.date],
    ["Cabin", cabinName(j.query.cabin)],
    ["Provider", j.query.provider.toUpperCase()],
    ["Created", date(j.createdAt)],
    ["Last update", date(j.updatedAt)],
    [
      "Duration",
      duration(
        (["running", "queued"].includes(j.status)
          ? Date.now()
          : new Date(j.updatedAt).getTime()) - new Date(j.createdAt).getTime(),
      ),
    ],
    ["Flights", String(j.result?.itineraries?.length || 0)],
    [
      "Result",
      j.result?.complete ? "Complete" : j.result ? "Partial" : "No result",
    ],
    ["Recovery events", String(j.recoveries?.length || 0)],
  ])
    facts.append(el("dt", k), el("dd", v));
  body.append(facts);
  if (j.error) {
    const box = el("div", undefined, "detail-error");
    box.append(el("strong", j.error.code), el("p", j.error.message));
    if (j.error.stage) box.append(el("small", "Stopped at: " + j.error.stage));
    if (j.error.outcomeUnknown)
      box.append(
        el(
          "p",
          "Outcome uncertain. The submission was not automatically replayed.",
        ),
      );
    body.append(box);
  }
  if (j.recoveries?.length) {
    body.append(el("h3", "Automatic recoveries"));
    for (const r of j.recoveries) {
      const row = el("div", undefined, "recovery-row");
      row.append(el("strong", "Attempt " + r.attempt), cell(r.stage, r.code));
      body.append(row);
    }
  }
  if (j.result?.itineraries?.length) {
    body.append(el("h3", "Returned flights"));
    body.append(
      table(
        ["Flight", "Fares", "Miles + cash"],
        j.result.itineraries.map((i) => {
          const fares = i.fares || [];
          return [
            cell(
              (i.segments || []).map((s) => s.flightNumber).join(" → ") || i.id,
              (i.segments || [])
                .map(
                  (s) =>
                    s.origin + " → " + s.destination + " · " + s.departureLocal,
                )
                .join(" / "),
            ),
            fares.map((f) => f.name).join(" · ") || String(fares.length),
            fares
              .map((f) =>
                f.milesCash
                  ? formatNumber(f.milesCash.miles) +
                    " miles + " +
                    f.milesCash.remainingCash.amount +
                    " " +
                    f.milesCash.remainingCash.currency
                  : "No completed mileage quote",
              )
              .join(" · "),
          ];
        }),
      ),
    );
  }
  const raw = el("details"),
    pre = el("pre", JSON.stringify(j, null, 2));
  raw.append(el("summary", "Structured result & diagnostics"), pre);
  body.append(raw);
  if (!$("search-detail").open) $("search-detail").showModal();
}
$("close-detail").onclick = () => $("search-detail").close();
$("search-detail").addEventListener("click", (e) => {
  if (e.target === $("search-detail")) {
    const r = e.target.getBoundingClientRect();
    if (
      e.clientX < r.left ||
      e.clientX > r.right ||
      e.clientY < r.top ||
      e.clientY > r.bottom
    )
      e.target.close();
  }
});
$("analytics-range").onchange = () => {
  historyPage = 1;
  refresh().catch((e) => message(e.message));
};
$("refresh-dashboard").onclick = async () => {
  const button = $("refresh-dashboard");
  button.disabled = true;
  button.classList.add("is-loading");
  try {
    await refresh();
  } catch (e) {
    message(e.message);
  } finally {
    button.disabled = false;
    button.classList.remove("is-loading");
  }
};
$("pause-refresh").onclick = () => {
  dashboardPaused = !dashboardPaused;
  $("pause-refresh").textContent = dashboardPaused
    ? "Resume live"
    : "Pause live";
  $("last-sync").classList.toggle("paused", dashboardPaused);
  if (dashboardPaused) $("last-sync").textContent = "Live updates paused";
  else refresh().catch((e) => message(e.message));
};
$("history-filters").onsubmit = async (e) => {
  e.preventDefault();
  for (const key of Object.keys(historyFilters))
    historyFilters[key] = $("history-" + (key === "q" ? "query" : key)).value;
  historyPage = 1;
  await loadHistory().catch((e) => message(e.message));
};
$("reset-history").onclick = () => {
  $("history-filters").reset();
  historyFilters = {
    q: "",
    status: "",
    provider: "",
    mode: "",
    sort: "newest",
  };
  historyPage = 1;
  loadHistory().catch((e) => message(e.message));
};
$("history-limit").onchange = () => {
  historyPage = 1;
  loadHistory().catch((e) => message(e.message));
};
$("export-history").onclick = () => {
  location.assign("/api/admin/export?" + historyQuery());
};
window.addEventListener("hashchange", switchView);
switchView();
