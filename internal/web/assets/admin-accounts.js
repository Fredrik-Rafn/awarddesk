"use strict";
const accountStates = {
  ready: "Ready",
  attention: "Needs attention",
  preparing: "Preparing",
  queued: "Queued",
  searching: "Searching",
  idle: "Not ready",
  unconfigured: "Unconfigured",
  disabled: "Disabled",
};
let accountVisibleLimit = 25,
  accountData = null;
const accountRows = new Map();
function accountModels(data) {
  const sessions = new Map(
    (data.sessions || []).map((item) => [item.id, item]),
  );
  const setups = new Map();
  for (const item of data.accountSetups || [])
    if (item.accountId) {
      const previous = setups.get(item.accountId);
      if (
        !previous ||
        Date.parse(item.updatedAt) >= Date.parse(previous.updatedAt)
      )
        setups.set(item.accountId, item);
    }
  const ids = new Set((data.accounts || []).map((item) => item.id));
  const models = (data.accounts || []).map((account) => {
    const session = sessions.get(account.id) || {},
      setup = setups.get(account.id);
    let state = "idle";
    if (!account.enabled || session.state === "disabled") state = "disabled";
    else if (
      account.configured === false ||
      session.configured === false ||
      !account.email ||
      !account.outlookEmail ||
      !account.clientId
    )
      state = "unconfigured";
    else if (session.queued || session.state === "queued") state = "queued";
    else if (session.busy)
      state = session.state === "searching" ? "searching" : "preparing";
    else if (session.needsAttention || session.state === "failed")
      state = "attention";
    else if (
      session.session?.ready &&
      (!session.nextAttempt || new Date(session.nextAttempt) <= new Date())
    )
      state = "ready";
    else if (setup?.status === "queued") state = "queued";
    else if (setup?.status === "running") state = "preparing";
    else if (setup && ["failed", "attention"].includes(setup.status))
      state = "attention";
    return { key: "account:" + account.id, account, session, setup, state };
  });
  const seen = new Set();
  for (const setup of [...(data.accountSetups || [])].sort(
    (a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt),
  )) {
    const identity = setup.accountId || setup.id;
    if ((setup.accountId && ids.has(setup.accountId)) || seen.has(identity))
      continue;
    seen.add(identity);
    models.push({
      key: "setup:" + setup.id,
      setup,
      state:
        setup.status === "queued"
          ? "queued"
          : setup.status === "running"
            ? "preparing"
            : "attention",
    });
  }
  return models;
}
function accountCells(model) {
  const { account, session = {}, setup, state } = model;
  const status = el("div");
  status.append(
    el("span", accountStates[state], "account-state account-state-" + state),
  );
  const note =
    state === "unconfigured"
      ? "Complete the account credentials to prepare it."
      : !account
        ? setup.error || setup.message
        : session.message ||
          (state === "attention" ? setup?.error || setup?.message : "");
  if (note) status.append(el("small", note));
  if (["queued", "preparing"].includes(state)) {
    const track = el("div", undefined, "job-progress account-progress");
    track.setAttribute("aria-hidden", "true");
    track.append(el("span", undefined, "indeterminate"));
    status.append(track);
  }
  const controls = el("div", undefined, "row-actions");
  const inProgress = ["preparing", "queued", "searching"].includes(state);
  if (setup && ["failed", "attention"].includes(setup.status) && !inProgress)
    controls.append(
      action(
        setup.canRestart
          ? "Restart signup"
          : setup.status === "attention"
            ? "Resume verification"
            : "Retry setup",
        () => retryRegistration(setup),
      ),
    );
  if (account) {
    controls.append(action("Edit", () => editAccount(account)));
    const prepare = action("Prepare", async () => {
      await api("/api/admin/prepare", {
        method: "POST",
        body: JSON.stringify({ id: account.id }),
      });
      message("Session preparation queued", "success");
      await refresh();
    });
    prepare.disabled =
      inProgress || ["ready", "disabled", "unconfigured"].includes(state);
    controls.append(prepare);
    const invalidate = action("Invalidate", async () => {
      await api("/api/admin/invalidate", {
        method: "POST",
        body: JSON.stringify({ id: account.id }),
      });
      message("Saved session removed", "success");
      await refresh();
    });
    invalidate.disabled = inProgress || !session.session?.present;
    controls.append(invalidate);
    controls.append(
      action("Delete", async () => {
        if (
          !(await confirmAction({
            title: "Delete this account?",
            message:
              "This removes the account and its saved login session. Historical searches and statistics will be kept.",
            confirmLabel: "Delete account",
          }))
        )
          return;
        await api("/api/admin/accounts?id=" + encodeURIComponent(account.id), {
          method: "DELETE",
        });
        await refresh();
      }),
    );
    if (inProgress)
      for (const control of controls.children) control.disabled = true;
  }
  return [
    cell(account?.label || "Account setup", account?.email || setup.email),
    status,
    account
      ? cell(
          session.session?.present
            ? date(session.session.expiresAt)
            : "No saved session",
          session.session?.expirySource,
        )
      : cell("Membership unconfirmed", "No login attempted"),
    cell(
      date(account ? session.checkedAt : setup.updatedAt),
      account ? session.stage : setup.stage,
    ),
    controls,
  ];
}
function renderAccounts(data) {
  accountData = data;
  const all = accountModels(data),
    counts = Object.fromEntries(
      Object.keys(accountStates).map((key) => [key, 0]),
    );
  for (const model of all) counts[model.state]++;
  const summary = $("account-summary");
  const signature = JSON.stringify(counts);
  if (summary.dataset.counts !== signature) {
    summary.dataset.counts = signature;
    summary.replaceChildren(
      ...Object.entries(accountStates).map(([state, label]) => {
        const item = el(
          "div",
          undefined,
          "account-summary-item account-state-" + state,
        );
        item.append(el("strong", String(counts[state])), el("span", label));
        return item;
      }),
    );
  }
  const query = $("account-query").value.trim().toLowerCase(),
    filter = $("account-status").value;
  const filtered = all.filter(
    (model) =>
      (filter === "all" || model.state === filter) &&
      [model.account?.label, model.account?.email, model.setup?.email].some(
        (value) => value?.toLowerCase().includes(query),
      ),
  );
  const visible = filtered.slice(0, accountVisibleLimit),
    list = $("account-list");
  if (!list.firstChild) {
    list.append(
      table(
        ["Account", "Status", "Session expires", "Last check", "Actions"],
        [],
      ),
    );
    list.querySelector("tbody").replaceChildren();
  }
  const body = list.querySelector("tbody"),
    keys = new Set(visible.map((model) => model.key));
  for (const [key, row] of accountRows)
    if (!keys.has(key)) {
      row.remove();
      accountRows.delete(key);
    }
  body.querySelector(".empty-cell")?.parentElement.remove();
  visible.forEach((model, index) => {
    let row = accountRows.get(model.key);
    const next = JSON.stringify(model);
    if (!row) {
      row = el("tr");
      accountRows.set(model.key, row);
    }
    if (row.dataset.snapshot !== next) {
      const focused = row.contains(document.activeElement),
        cells = accountCells(model);
      cells.forEach((content, cellIndex) => {
        if (focused && cellIndex === 4) {
          for (const control of row.children[4].querySelectorAll("button")) {
            const replacement = [...content.querySelectorAll("button")].find(
              (candidate) => candidate.textContent === control.textContent,
            );
            if (control.getAttribute("aria-busy") !== "true")
              control.disabled = !replacement || replacement.disabled;
          }
          return;
        }
        const td = el("td");
        td.append(content);
        if (row.children[cellIndex]) row.children[cellIndex].replaceWith(td);
        else row.append(td);
      });
      // Retry replacing controls after focus leaves, while status cells stay live.
      if (!focused) row.dataset.snapshot = next;
    }
    if (body.children[index] !== row)
      body.insertBefore(row, body.children[index] || null);
  });
  if (!visible.length) {
    const row = el("tr"),
      td = el(
        "td",
        all.length
          ? "No accounts match your filters."
          : "No accounts yet. Start with bulk onboarding below.",
        "empty-cell",
      );
    td.colSpan = 5;
    row.append(td);
    body.append(row);
  }
  $("account-count").textContent =
    "Showing " +
    visible.length +
    " of " +
    filtered.length +
    " accounts" +
    (filtered.length !== all.length ? " · " + all.length + " total" : "");
  $("account-scroll-sentinel").hidden = visible.length >= filtered.length;
  const eligible = all.filter(
    (model) =>
      model.account &&
      ![
        "ready",
        "disabled",
        "unconfigured",
        "preparing",
        "queued",
        "searching",
      ].includes(model.state),
  ).length;
  const prepareAll = $("prepare-all-accounts");
  const pendingSetups = (data.accountSetups || []).filter((item) =>
    ["attention", "failed"].includes(item.status),
  ).length;
  const retryAll = $("retry-all-setups");
  if (retryAll.getAttribute("aria-busy") !== "true")
    retryAll.disabled = !pendingSetups;
  retryAll.textContent =
    "Retry / continue setups" +
    (pendingSetups ? " (" + pendingSetups + ")" : "");
  if (prepareAll.getAttribute("aria-busy") !== "true")
    prepareAll.disabled = !eligible;
  prepareAll.textContent =
    "Prepare all accounts" + (eligible ? " (" + eligible + ")" : "");
}
function resetAccountBrowser() {
  accountData = null;
  accountVisibleLimit = 25;
  accountRows.clear();
  $("account-query").value = "";
  $("account-status").value = "all";
  $("account-status").dispatchEvent(new Event("change", { bubbles: true }));
  $("account-summary").replaceChildren();
  delete $("account-summary").dataset.counts;
  $("account-count").textContent = "";
  $("account-scroll-sentinel").hidden = true;
}
function filterAccounts() {
  accountVisibleLimit = 25;
  $("account-scroll").scrollTop = 0;
  if (accountData) renderAccounts(accountData);
}
$("account-query").addEventListener("input", filterAccounts);
$("account-status").addEventListener("change", filterAccounts);
$("reset-account-filters").onclick = () => {
  $("account-query").value = "";
  $("account-status").value = "all";
  $("account-status").dispatchEvent(new Event("change", { bubbles: true }));
};
function showMoreAccounts() {
  accountVisibleLimit += 25;
  if (accountData) renderAccounts(accountData);
}
$("load-more-accounts").onclick = showMoreAccounts;
if ("IntersectionObserver" in window)
  new IntersectionObserver(
    (entries) => {
      if (
        entries.some((entry) => entry.isIntersecting) &&
        accountData &&
        !$("account-scroll-sentinel").hidden
      )
        showMoreAccounts();
    },
    { root: $("account-scroll"), rootMargin: "0px 0px 160px 0px" },
  ).observe($("account-scroll-sentinel"));
$("prepare-all-accounts").onclick = async () => {
  const button = $("prepare-all-accounts");
  button.disabled = true;
  button.classList.add("is-loading");
  button.setAttribute("aria-busy", "true");
  try {
    const result = await api("/api/admin/prepare-all", { method: "POST" });
    message(
      result.queued +
        " accounts queued · " +
        result.skipped +
        " skipped" +
        (result.unconfigured
          ? " · " + result.unconfigured + " need configuration"
          : ""),
      "success",
    );
    await refresh();
  } catch (error) {
    message(error.message);
  } finally {
    button.classList.remove("is-loading");
    button.removeAttribute("aria-busy");
    if (accountData) renderAccounts(accountData);
    else button.disabled = false;
  }
};
