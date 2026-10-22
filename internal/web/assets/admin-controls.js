"use strict";
let cachePolicyEnabled = true;
function renderPagination(container, page, pages, onChange) {
  const signature = page + ":" + pages;
  if (container.dataset.pages === signature) return;
  container.dataset.pages = signature;
  container.replaceChildren();
  const values = [...new Set([1, page - 1, page, page + 1, pages])]
    .filter((value) => value > 0 && value <= pages)
    .sort((a, b) => a - b);
  function button(label, target, name) {
    const control = el("button", label, "page-button");
    control.type = "button";
    control.setAttribute("aria-label", name || "Page " + target);
    if (target === page) control.setAttribute("aria-current", "page");
    control.disabled = target < 1 || target > pages || target === page;
    control.onclick = async () => {
      if (container.getAttribute("aria-busy") === "true") return;
      container.setAttribute("aria-busy", "true");
      const controls = [...container.querySelectorAll("button")].map((node) => [
        node,
        node.disabled,
      ]);
      controls.forEach(([node]) => {
        node.disabled = true;
      });
      try {
        await onChange(target);
      } finally {
        container.removeAttribute("aria-busy");
        controls.forEach(([node, disabled]) => {
          node.disabled = disabled;
        });
      }
    };
    container.append(control);
  }
  if (pages > 1) button("‹", page - 1, "Previous page");
  let previous = 0;
  for (const value of values) {
    if (previous && value - previous > 1) {
      const gap = el("span", "…", "page-gap");
      gap.setAttribute("aria-hidden", "true");
      container.append(gap);
    }
    button(String(value), value);
    previous = value;
  }
  if (pages > 1) button("›", page + 1, "Next page");
}
function confirmAction({ title, message, confirmLabel = "Continue" }) {
  const dialog = $("confirm-dialog");
  if (dialog.open) return Promise.resolve(false);
  $("confirm-title").textContent = title;
  $("confirm-message").textContent = message;
  $("confirm-accept").textContent = confirmLabel;
  dialog.returnValue = "";
  return new Promise((resolve) => {
    dialog.addEventListener(
      "close",
      () => resolve(dialog.returnValue === "confirm"),
      { once: true },
    );
    dialog.showModal();
    $("confirm-cancel").focus();
  });
}
function pillSwitch(label, enabled, onChange) {
  const button = el("button", undefined, "pill-switch");
  button.type = "button";
  button.setAttribute("role", "switch");
  button.setAttribute("aria-label", label);
  const track = el("span", undefined, "switch-track");
  track.append(el("span"));
  button.append(track, el("span", undefined, "switch-label"));
  updateSwitch(button, enabled);
  button.onclick = async () => {
    if (button.disabled) return;
    button.disabled = true;
    try {
      await onChange(button.getAttribute("aria-checked") !== "true", button);
    } catch (e) {
      message(e.message);
    } finally {
      button.disabled = false;
    }
  };
  return button;
}
function updateSwitch(button, enabled) {
  button.setAttribute("aria-checked", String(enabled));
  button.querySelector(".switch-label").textContent = enabled
    ? "Enabled"
    : "Disabled";
}
function syncCachePolicy(policy) {
  cachePolicyEnabled = policy.enabled;
  for (const button of document.querySelectorAll(
    "[data-cache-switch],#flight-cache-toggle",
  ))
    updateSwitch(button, policy.enabled);
  $("cache-policy-description").textContent = policy.enabled
    ? "Complete flight results are reused for " +
      policy.ttl +
      ". Refresh live always skips the cache."
    : "Every flight search runs live. No flight results are cached. Historical searches are kept.";
  $("clear-cache").disabled = !policy.enabled;
}
async function changeCachePolicy(enabled) {
  if (!enabled) {
    const yes = await confirmAction({
      title: "Disable flight caching?",
      message:
        "This immediately deletes all cached flight results. New and ongoing searches will not save to the cache while it is disabled. Your complete search history, saved login sessions and statistics will be kept.",
      confirmLabel: "Disable & clear cache",
    });
    if (!yes) return;
  }
  const policy = await api("/api/admin/cache-policy", {
    method: "PUT",
    body: JSON.stringify({ enabled, confirmClear: !enabled }),
  });
  syncCachePolicy(policy);
  $("cache-details").hidden = true;
  $("cache-json").textContent = "";
  message(
    enabled
      ? "Flight caching enabled"
      : "Flight caching disabled. Cached flights cleared; historical data kept.",
    "success",
  );
  if (settingDefinitions.FLIGHT_CACHE_ENABLED)
    settingDefinitions.FLIGHT_CACHE_ENABLED.value = String(policy.enabled);
  const ttlInput = document.getElementById("setting-EVA_RESULT_CACHE_TTL");
  if (enabled && ttlInput && /^0(?:[a-z]+)?$/.test(ttlInput.value))
    ttlInput.value = policy.ttl;
  await refresh();
}
const settingInfo = {
  EVA_RESULT_CACHE_TTL: [
    "Cache lifetime",
    "How long complete flight results may be reused. Example: 30m.",
  ],
  EVA_SESSION_MAX_AGE: [
    "Maximum session age",
    "Sessions retire five minutes before this limit or their earlier cookie deadline.",
  ],
  SEARCH_CONCURRENCY: [
    "Concurrent searches",
    "Maximum independent accounts working at once. Each account stays exclusive.",
  ],
  EVA_BACKGROUND_ENABLED: [
    "Prepare EVA sessions automatically",
    "Keep enabled EVA accounts signed in and periodically verify idle sessions.",
  ],
  ANA_BACKGROUND_ENABLED: [
    "Prepare ANA sessions automatically",
    "Run periodic ANA member-session checks. Requires a server restart.",
  ],
  EVA_IMPERVA_SOLVER: [
    "Booking security provider",
    "Auto prefers Hyper, with AnySolver fallback for generation-service errors.",
  ],
  HYPER_API_KEY: [
    "Hyper Solutions API key",
    "Used for airline browser security.",
  ],
  ANYSOLVER_API_KEY: [
    "AnySolver API key",
    "Used for CAPTCHA verification and supported solver fallback.",
  ],
  ANA_MEMBER_NUMBER: [
    "ANA member number",
    "Optional ANA integration. Requires a restart.",
  ],
  ANA_PASSWORD: [
    "ANA password",
    "Optional ANA integration. Leave empty to keep the saved password.",
  ],
  BA_EMAIL: [
    "British Airways email",
    "Experimental integration. Requires a restart.",
  ],
  BA_PASSWORD: [
    "British Airways password",
    "Leave empty to keep the saved password.",
  ],
  AWARDDESK_PROXY: [
    "Legacy direct proxy",
    "Optional proxy for legacy integrations. EVA uses the proxy pool.",
  ],
};
function renderSettingsFields(definitions) {
  const fields = $("setting-fields");
  fields.replaceChildren();
  const groups = new Map();
  function group(key) {
    if (groups.has(key)) return groups.get(key);
    const collapsed = key === "Legacy integrations" || key === "Other settings";
    const box = el(
        collapsed ? "details" : "section",
        undefined,
        "settings-group",
      ),
      header = el(collapsed ? "summary" : "h3", key);
    box.append(header);
    const grid = el("div", undefined, "settings-group-fields");
    box.append(grid);
    fields.append(box);
    groups.set(key, grid);
    return grid;
  }
  const cacheRow = el("div", undefined, "setting-row cache-setting-row"),
    copy = el("div", undefined, "setting-copy");
  copy.append(
    el("strong", "Flight caching"),
    el(
      "p",
      "Applies immediately. Disabling clears cached flights after confirmation; history stays forever.",
    ),
  );
  const cacheToggle = pillSwitch(
    "Flight caching",
    cachePolicyEnabled,
    changeCachePolicy,
  );
  cacheToggle.dataset.cacheSwitch = "true";
  cacheRow.append(copy, cacheToggle);
  group("Search behavior").append(cacheRow);
  const entries = Object.entries(definitions)
    .filter(
      ([k]) =>
        k !== "FLIGHT_CACHE_ENABLED" &&
        !k.startsWith("BA_") &&
        !k.startsWith("ANA_"),
    )
    .sort(([a], [b]) => a.localeCompare(b));
  for (const [key, s] of entries) {
    const section =
      key === "EVA_RESULT_CACHE_TTL" ||
      key === "SEARCH_CONCURRENCY" ||
      key === "EVA_SESSION_MAX_AGE"
        ? "Search behavior"
        : key.endsWith("BACKGROUND_ENABLED")
          ? "Session preparation"
          : key === "HYPER_API_KEY" ||
              key === "ANYSOLVER_API_KEY" ||
              key === "EVA_IMPERVA_SOLVER"
            ? "Provider services"
            : key.startsWith("ANA_") ||
                key.startsWith("BA_") ||
                key === "AWARDDESK_PROXY"
              ? "Legacy integrations"
              : "Other settings";
    const row = el("div", undefined, "setting-row"),
      copy = el("div", undefined, "setting-copy"),
      [name, hint] = settingInfo[key] || [key, "Custom workspace setting."];
    const label = el("label", name);
    label.htmlFor = "setting-" + key;
    label.title = key;
    copy.append(label, el("p", hint));
    const control = el("div", undefined, "setting-control"),
      input = el("input");
    input.id = "setting-" + key;
    input.name = key;
    input.autocomplete = "off";
    input.value = s.value;
    if (!s.secret && (s.value === "true" || s.value === "false")) {
      input.type = "hidden";
      const button = pillSwitch(name, s.value === "true", async (enabled) => {
        input.value = String(enabled);
        input.dispatchEvent(new Event("input", { bubbles: true }));
        updateSwitch(button, enabled);
      });
      button.id = input.id;
      input.removeAttribute("id");
      control.append(input, button);
    } else if (key === "EVA_IMPERVA_SOLVER") {
      const select = el("select");
      select.name = key;
      select.id = input.id;
      for (const [value, title] of [
        ["auto", "Auto · Hyper first"],
        ["hyper", "Hyper Solutions"],
        ["anysolver", "AnySolver"],
      ]) {
        const option = el("option", title);
        option.value = value;
        select.append(option);
      }
      select.value = s.value;
      control.append(select);
    } else {
      input.type = s.secret
        ? "password"
        : key === "SEARCH_CONCURRENCY"
          ? "number"
          : "text";
      if (key === "SEARCH_CONCURRENCY") {
        input.min = "1";
        input.max = "512";
        input.step = "1";
      }
      input.placeholder = s.secret ? "Not configured" : "";
      control.append(input);
    }
    if (s.secret) {
      const status = el(
        "span",
        s.configured ? "Configured" : "Not configured",
        "secret-status " + (s.configured ? "configured" : ""),
      );
      copy.append(status);
    }
    row.append(copy, control);
    group(section).append(row);
  }
  for (const name of [
    "Search behavior",
    "Session preparation",
    "Provider services",
    "Legacy integrations",
    "Other settings",
  ]) {
    const grid = groups.get(name);
    if (grid) fields.append(grid.parentElement);
  }
  enhanceSecretInputs(fields);
}
function enhanceSecretInputs(root) {
  for (const input of root.querySelectorAll(
    'input[type="password"]:not([data-secret-input])',
  )) {
    input.dataset.secretInput = "true";
    const wrap = document.createElement("div");
    wrap.className = "secret-input";
    input.before(wrap);
    wrap.append(input);
    const button = document.createElement("button");
    button.type = "button";
    button.className = "secret-eye";
    const name = input.labels?.[0]?.textContent.trim() || "value";
    button.setAttribute("aria-label", "Show " + name);
    button.setAttribute("aria-pressed", "false");
    button.innerHTML =
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/></svg>';
    button.onclick = () => {
      const show = input.type === "password";
      input.type = show ? "text" : "password";
      button.setAttribute("aria-label", (show ? "Hide " : "Show ") + name);
      button.setAttribute("aria-pressed", String(show));
    };
    wrap.append(button);
  }
}
function resetSecretInputs(root) {
  for (const input of root.querySelectorAll("[data-secret-input]")) {
    input.type = "password";
    const button = input.parentElement.querySelector(".secret-eye");
    if (!button) continue;
    button.setAttribute("aria-pressed", "false");
    button.setAttribute(
      "aria-label",
      "Show " + (input.labels?.[0]?.textContent.trim() || "value"),
    );
  }
}
document.addEventListener("DOMContentLoaded", () => {
  enhanceSecretInputs(document);
  for (const form of document.querySelectorAll("form")) {
    form.addEventListener("reset", () => resetSecretInputs(form));
    const handler = form.onsubmit;
    if (!handler) continue;
    let pending = false;
    form.onsubmit = async (event) => {
      event.preventDefault();
      if (pending) return;
      pending = true;
      const button = form.querySelector('[type="submit"]');
      const wasDisabled = button?.disabled;
      if (button) {
        button.disabled = true;
        button.classList.add("is-loading");
      }
      form.setAttribute("aria-busy", "true");
      try {
        await handler.call(form, event);
      } finally {
        pending = false;
        form.removeAttribute("aria-busy");
        if (button) {
          button.disabled = wasDisabled;
          button.classList.remove("is-loading");
        }
      }
    };
  }
  $("settings-form").addEventListener("input", () =>
    $("settings-form").classList.add("has-unsaved"),
  );
  $("settings-form").addEventListener("change", () =>
    $("settings-form").classList.add("has-unsaved"),
  );
});
document.getElementById("confirm-cancel").onclick = () =>
  $("confirm-dialog").close("cancel");
document.getElementById("confirm-accept").onclick = () =>
  $("confirm-dialog").close("confirm");
document.getElementById("flight-cache-toggle").onclick = async () => {
  const button = $("flight-cache-toggle");
  button.disabled = true;
  try {
    await changeCachePolicy(!cachePolicyEnabled);
  } catch (e) {
    message(e.message);
  } finally {
    button.disabled = false;
  }
};
