"use strict";
const $ = (id) => document.getElementById(id);
let csrf = "",
  overview = null,
  settingDefinitions = {},
  polling = false;
const date = (v) =>
  v && !v.startsWith("0001") ? new Date(v).toLocaleString() : "—";
function el(tag, text, cls) {
  const n = document.createElement(tag);
  if (text !== undefined) n.textContent = text;
  if (cls) n.className = cls;
  return n;
}
function message(text) {
  $("message").textContent = text;
}
async function api(path, options = {}) {
  const r = await fetch(path, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": csrf,
      ...options.headers,
    },
  });
  if (r.status === 204) return null;
  const b = await r.json();
  if (!r.ok) {
    if (r.status === 401 && path !== "/api/admin/session") showLogin();
    throw Error(b.error || b.message || "Request failed");
  }
  return b;
}
function showLogin() {
  polling = false;
  $("workspace").hidden = true;
  $("login-panel").hidden = false;
  $("proxy-list").value = "";
  $("setting-fields").replaceChildren();
  $("account-form").reset();
  overview = null;
  if (typeof clearDashboard === "function") clearDashboard();
  for (const id of ["account-list", "flight-list", "job-list", "cache-json"])
    $(id).replaceChildren();
}
function action(label, fn) {
  const b = el("button", label, "secondary");
  b.type = "button";
  b.onclick = async () => {
    b.disabled = true;
    try {
      await fn();
    } catch (e) {
      message(e.message);
    } finally {
      b.disabled = false;
    }
  };
  return b;
}
function table(headers, rows) {
  const wrap = el("div", undefined, "table-wrap"),
    t = el("table"),
    head = el("tr");
  for (const h of headers) head.append(el("th", h));
  const thead = el("thead"),
    body = el("tbody");
  thead.append(head);
  for (const cells of rows) {
    const tr = el("tr");
    for (const cell of cells) {
      const td = el("td");
      if (cell instanceof Node) td.append(cell);
      else td.textContent = cell;
      tr.append(td);
    }
    body.append(tr);
  }
  if (!rows.length) {
    const tr = el("tr"),
      td = el("td", "No records to show yet.", "empty-cell");
    td.colSpan = headers.length;
    tr.append(td);
    body.append(tr);
  }
  t.append(thead, body);
  wrap.append(t);
  return wrap;
}
function cell(main, sub) {
  const d = el("div");
  d.append(el("span", main));
  if (sub) d.append(el("small", sub));
  return d;
}
function render(data) {
  overview = data;
  $("proxy-count").textContent =
    String(data.proxyCount || 0) + " configured endpoints";
  const status = new Map(data.sessions.map((s) => [s.id, s]));
  $("account-list").replaceChildren(
    table(
      ["Account", "Status", "Session expires", "Last check", "Actions"],
      data.accounts.map((a) => {
        const s = status.get(a.id) || {},
          state = s.state || "idle",
          p = el("div");
        p.append(el("span", state, "pill " + state));
        if (s.message) p.append(el("small", s.message));
        if (s.needsAttention)
          p.append(el("small", "Needs attention · use Prepare to retry"));
        else if (s.nextAttempt && new Date(s.nextAttempt) > new Date())
          p.append(el("small", "Next attempt: " + date(s.nextAttempt)));
        const controls = el("div");
        controls.append(
          action("Edit", () => editAccount(a)),
          action("Prepare", async () => {
            await api("/api/admin/prepare", {
              method: "POST",
              body: JSON.stringify({ id: a.id }),
            });
            message("Session preparation queued");
            await refresh();
          }),
          action("Invalidate", async () => {
            await api("/api/admin/invalidate", {
              method: "POST",
              body: JSON.stringify({ id: a.id }),
            });
            message("Saved session removed");
            await refresh();
          }),
          action("Delete", async () => {
            if (!confirm("Delete this account and its saved session?")) return;
            await api("/api/admin/accounts?id=" + encodeURIComponent(a.id), {
              method: "DELETE",
            });
            await refresh();
          }),
        );
        for (const b of controls.querySelectorAll("button"))
          b.disabled = !!s.busy;
        return [
          cell(a.label, a.email),
          p,
          cell(
            s.session?.present ? date(s.session.expiresAt) : "No saved session",
            s.session?.expirySource,
          ),
          cell(date(s.checkedAt), s.stage),
          controls,
        ];
      }),
    ),
  );
  $("flight-list").replaceChildren(
    table(
      ["Route", "Departure / cabin", "Flights", "Fetched / expires", "Actions"],
      data.flights.map((f) => {
        const c = el("div");
        c.append(
          action("View", async () => {
            const result = await api(
              "/api/admin/cache?key=" + encodeURIComponent(f.key),
            );
            $("cache-details").hidden = false;
            $("cache-details").open = true;
            $("cache-json").textContent = JSON.stringify(result, null, 2);
          }),
          action("Remove", async () => {
            await api("/api/admin/cache?key=" + encodeURIComponent(f.key), {
              method: "DELETE",
            });
            await refresh();
          }),
        );
        return [
          f.query.origin + " → " + f.query.destination,
          cell(f.query.date, f.query.cabin),
          String(f.count),
          cell(
            date(f.fetchedAt),
            (f.expired ? "Expired · " : "Until ") + date(f.expiresAt),
          ),
          c,
        ];
      }),
    ),
  );
  if (typeof renderHealth === "function") renderHealth(data);
}
async function refresh() {
  if (!polling) return;
  if (typeof refreshDashboard === "function") await refreshDashboard();
  else render(await api("/api/admin/overview"));
}
function editAccount(a = { enabled: true }) {
  const form = $("account-form");
  form.reset();
  for (const k of ["id", "label", "email", "outlookEmail", "clientId"])
    form.elements.namedItem(k).value = a[k] || "";
  form.elements.enabled.checked = a.enabled;
  form.elements.email.readOnly = !!a.id;
  form.elements.password.required = !a.id;
  form.elements.refreshToken.required = !a.id;
  $("account-heading").textContent = a.id
    ? "Edit EVA account"
    : "Add EVA account";
  form.hidden = false;
  form.scrollIntoView({ behavior: "smooth", block: "center" });
}
async function loadSettings() {
  settingDefinitions = await api("/api/admin/settings");
  const fields = $("setting-fields");
  fields.replaceChildren();
  for (const [key, s] of Object.entries(settingDefinitions).sort(([a], [b]) =>
    a.localeCompare(b),
  )) {
    const label = el("label", key),
      input = el("input");
    input.name = key;
    input.type = s.secret ? "password" : "text";
    input.autocomplete = "off";
    input.value = s.value;
    input.placeholder =
      s.secret && s.configured ? "Configured · leave blank to keep" : "";
    label.append(input);
    if (s.secret) {
      const check = el("label", undefined, "check"),
        box = el("input");
      box.type = "checkbox";
      box.dataset.clear = key;
      check.append(box, el("span", "Clear saved value"));
      label.append(check);
    }
    fields.append(label);
  }
}
$("edit-proxies").onclick = async () => {
  try {
    const b = await api("/api/admin/proxies");
    $("proxy-list").value = b.value;
    $("proxy-form").hidden = false;
    $("edit-proxies").hidden = true;
  } catch (e) {
    message(e.message);
  }
};
async function openWorkspace() {
  $("admin-password").value = "";
  $("login-panel").hidden = true;
  $("workspace").hidden = false;
  polling = true;
  try {
    await Promise.all([refresh(), loadSettings()]);
  } catch (e) {
    message(e.message);
  }
}
$("login-form").onsubmit = async (e) => {
  e.preventDefault();
  try {
    await api("/api/admin/session", {
      method: "POST",
      body: JSON.stringify({ password: $("admin-password").value }),
    });
    message("");
    await openWorkspace();
  } catch (e) {
    message(e.message);
  }
};
$("logout").onclick = async () => {
  try {
    await api("/api/admin/session", { method: "DELETE" });
    showLogin();
  } catch (e) {
    message(e.message);
  }
};
$("new-account").onclick = () => editAccount();
$("cancel-account").onclick = () => {
  $("account-form").hidden = true;
  $("account-form").reset();
};
$("account-form").onsubmit = async (e) => {
  e.preventDefault();
  const form = e.currentTarget,
    a = Object.fromEntries(new FormData(form));
  a.enabled = form.elements.enabled.checked;
  try {
    await api("/api/admin/accounts", {
      method: "PUT",
      body: JSON.stringify(a),
    });
    form.reset();
    form.hidden = true;
    message("Account saved. Enabled accounts will be prepared automatically.");
    await refresh();
  } catch (e) {
    message(e.message);
  }
};
$("proxy-form").onsubmit = async (e) => {
  e.preventDefault();
  try {
    await api("/api/admin/proxies", {
      method: "PUT",
      body: JSON.stringify({ value: $("proxy-list").value }),
    });
    message("Proxy list saved");
    $("proxy-list").value = "";
    $("proxy-form").hidden = true;
    $("edit-proxies").hidden = false;
    await refresh();
  } catch (e) {
    message(e.message);
  }
};
$("settings-form").onsubmit = async (e) => {
  e.preventDefault();
  const values = {},
    clear = [];
  for (const input of $("setting-fields").querySelectorAll("input[name]")) {
    if (!settingDefinitions[input.name].secret || input.value)
      values[input.name] = input.value;
  }
  for (const c of $("setting-fields").querySelectorAll(
    "input[data-clear]:checked",
  ))
    clear.push(c.dataset.clear);
  if ($("new-setting-key").value.trim())
    values[$("new-setting-key").value.trim()] = $("new-setting-value").value;
  try {
    await api("/api/admin/settings", {
      method: "PUT",
      body: JSON.stringify({ values, clear }),
    });
    message("Settings saved");
    $("new-setting-key").value = "";
    $("new-setting-value").value = "";
    await loadSettings();
    await refresh();
  } catch (e) {
    message(e.message);
  }
};
$("clear-cache").onclick = async () => {
  if (!confirm("Clear all cached flight results?")) return;
  try {
    await api("/api/admin/cache", { method: "DELETE" });
    $("cache-details").hidden = true;
    $("cache-json").textContent = "";
    await refresh();
  } catch (e) {
    message(e.message);
  }
};
$("password-form").onsubmit = async (e) => {
  e.preventDefault();
  try {
    await api("/api/admin/password", {
      method: "PUT",
      body: JSON.stringify({ password: $("new-password").value }),
    });
    $("new-password").value = "";
    showLogin();
    message("Password changed. Sign in again.");
  } catch (e) {
    message(e.message);
  }
};
(async () => {
  try {
    csrf = (await api("/api/config")).csrf;
    if ((await api("/api/admin/session")).authenticated) await openWorkspace();
  } catch (e) {
    message(e.message);
  }
})();
setInterval(() => {
  if (polling && !document.hidden && !dashboardPaused)
    refresh().catch((e) => message(e.message));
}, 3000);
