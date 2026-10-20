"use strict";
const $ = (id) => document.getElementById(id);
const cabinNames = {
  economy: "Economy",
  premium_economy: "Premium economy",
  business: "Business",
  first: "First",
};
let config, currentJob, result, timer;
const number = (n) => new Intl.NumberFormat("en-GB").format(n);
const money = (m) => number(Number(m.amount)) + " " + m.currency;
const el = (tag, text, className) => {
  const n = document.createElement(tag);
  if (text !== undefined) n.textContent = text;
  if (className) n.className = className;
  return n;
};
async function api(path, options = {}) {
  const r = await fetch(path, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": config?.csrf || "",
      ...options.headers,
    },
  });
  const data = r.status === 204 ? null : await r.json();
  if (!r.ok) {
    const error = new Error(data.error || data.message || "Request failed");
    error.status = r.status;
    throw error;
  }
  return data;
}
function status(stage, message, detail, error = false) {
  $("status").hidden = false;
  $("status").classList.toggle("error", error);
  $("stage").textContent = stage.toUpperCase();
  $("status-message").textContent = message;
  $("status-detail").textContent = detail;
}
function busy(value) {
  $("submit").disabled = value || !config?.ready;
  $("cancel").hidden = !value;
}
function stop() {
  clearTimeout(timer);
  currentJob = null;
  sessionStorage.removeItem("awarddesk.job");
  busy(false);
}
async function poll() {
  if (!currentJob) return;
  const requestedJob = currentJob;
  try {
    const job = await api("/api/searches/" + requestedJob);
    if (currentJob !== requestedJob) return;
    if (job.status === "completed") {
      result = job.result;
      stop();
      $("status").hidden = true;
      showResults();
      return;
    }
    if (job.status === "failed" || job.status === "cancelled") {
      stop();
      status(
        job.status,
        job.error?.message || "Search cancelled",
        (job.status === "failed" ? "Failed at " + (job.error?.stage || job.progress?.stage || "provider request") + ". " : "") + "No payment or ticketing request was submitted.",
        job.status === "failed",
      );
      return;
    }
    status(
      job.progress.stage,
      job.progress.message,
      "Saved EVA sessions are reused. Fresh login and pricing can take a few minutes.",
    );
    timer = setTimeout(poll, 1800);
  } catch (e) {
    if (currentJob !== requestedJob) return;
    if (e.status === 404) {
      stop();
      status("expired", "Search is no longer available",
        "The server may have restarted or the saved result expired.", true);
      return;
    }
    status(
      "connection",
      "Reconnecting to your search",
      "The search may still be running. Checking again in five seconds.",
      true,
    );
    timer = setTimeout(poll, 5000);
  }
}
$("search").addEventListener("submit", async (e) => {
  e.preventDefault();
  $("results").hidden = true;
  $("empty").hidden = true;
  busy(true);
  status(
    "starting",
    "Preparing a fresh search",
    "Your credentials remain on the local server.",
  );
  try {
    const q = {
      provider: $("provider").value,
      origin: $("origin").value.trim().toUpperCase(),
      destination: $("destination").value.trim().toUpperCase(),
      date: $("date").value,
      cabin: $("cabin").value,
      adults: Number($("adults").value),
    };
    const job = await api("/api/searches", {
      method: "POST",
      body: JSON.stringify(q),
    });
    currentJob = job.id;
    location.hash = job.id;
    sessionStorage.setItem("awarddesk.job", job.id);
    poll();
  } catch (e) {
    busy(false);
    status(
      "unable to start",
      e.message,
      "Check the search fields and local configuration.",
      true,
    );
  }
});
$("cancel").addEventListener("click", async () => {
  if (!currentJob) return;
  try {
    await api("/api/searches/" + currentJob, { method: "DELETE" });
    await poll();
  } catch (e) {
    status("cancel", e.message, "Try cancelling again.", true);
  }
});
$("swap").addEventListener("click", () => {
  const a = $("origin").value,
    b = $("origin-hint").textContent;
  $("origin").value = $("destination").value;
  $("origin-hint").textContent = $("destination-hint").textContent;
  $("destination").value = a;
  $("destination-hint").textContent = b;
});
for (const id of ["origin", "destination"]) {
  const input = $(id),
    list = $(id + "-options");
  let delay,
    controller,
    items = [],
    index = -1;
  const close = () => {
    clearTimeout(delay);
    controller?.abort();
    list.hidden = true;
    input.setAttribute("aria-expanded", "false");
    input.removeAttribute("aria-activedescendant");
  };
  const select = (i) => {
    input.value = items[i].code;
    $(id + "-hint").textContent = items[i].city + " · " + items[i].name;
    close();
  };
  input.addEventListener("input", () => {
    $(id + "-hint").textContent = "Type an airport code or city";
    close();
    delay = setTimeout(async () => {
      const request = new AbortController();
      controller = request;
      try {
        items = await api(
          "/api/airports?q=" + encodeURIComponent(input.value),
          { signal: request.signal },
        );
        if (request.signal.aborted || controller !== request || document.activeElement !== input) return;
        index = -1;
        list.replaceChildren();
        items.forEach((a, i) => {
          const li = el("li", a.code + " · " + a.city);
          li.id = id + "-option-" + i;
          li.setAttribute("role", "option");
          li.setAttribute("aria-selected", "false");
          li.append(el("small", a.name + " · " + a.country));
          li.addEventListener("mousedown", (e) => {
            e.preventDefault();
            select(i);
          });
          list.append(li);
        });
        list.hidden = !items.length;
        input.setAttribute("aria-expanded", String(!!items.length));
      } catch (e) {
        if (e.name !== "AbortError" && controller === request) close();
      }
    }, 180);
  });
  input.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      close();
      return;
    }
    if (list.hidden) return;
    if (e.key === "Enter" && index >= 0) {
      e.preventDefault();
      select(index);
      return;
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      index =
        (index + (e.key === "ArrowDown" ? 1 : -1) + items.length) %
        items.length;
      [...list.children].forEach((n, i) =>
        n.setAttribute("aria-selected", String(i === index)),
      );
      input.setAttribute("aria-activedescendant", list.children[index].id);
      list.children[index].scrollIntoView({ block: "nearest" });
    }
  });
  input.addEventListener("blur", close);
}
function showResults() {
  document.body.classList.add("has-results");
  const q = result.query;
  $("results").hidden = false;
  $("result-route").textContent = q.origin + " → " + q.destination;
  $("result-title").textContent =
    result.itineraries.length + (q.provider === "eva" ? " flight options · Miles & Cash" : " award itineraries");
  $("result-meta").textContent =
    q.date +
    " · " +
    cabinNames[q.cabin] +
    " · " +
    q.adults +
    " adult" +
    (q.adults === 1 ? "" : "s") +
    " · " +
    (result.complete ? "Available quotes retrieved" : "Partial results");
  $("airline").replaceChildren(new Option("All airlines", "all"));
  [
    ...new Set(
      result.itineraries.flatMap((i) => i.segments.map((s) => s.airline)),
    ),
  ]
    .sort()
    .forEach((a) => $("airline").append(new Option(a, a)));
  $("warnings").replaceChildren(...result.warnings.map((w) => el("p", w)));
  $("warnings").hidden = !result.warnings.length;
  renderFlights();
}
function renderFlights() {
  const flights = $("flights");
  flights.replaceChildren();
  const list = result.itineraries
    .filter(
      (i) =>
        ($("stops").value !== "direct" || i.segments.length === 1) &&
        ($("airline").value === "all" ||
          i.segments.some((s) => s.airline === $("airline").value)),
    )
    .slice();
  const cost = (f) => f.pricingType === "miles_cash" ? Number(f.cashPrice?.amount ?? Infinity) : f.points;
  const cheapest = (i) => Math.min(...i.fares.map(cost));
  list.sort((a, b) =>
    $("sort").value === "points"
      ? cheapest(a) - cheapest(b)
      : a.segments[0].departureLocal.localeCompare(
          b.segments[0].departureLocal,
        ),
  );
  if (!list.length) {
    flights.append(
      el(
        "p",
        result.itineraries.length
          ? "No flights match these filters."
          : "No flights returned for this search.",
      ),
    );
    return;
  }
  for (const item of list) {
    const first = item.segments[0],
      last = item.segments.at(-1),
      fare = item.fares.reduce((a, b) => (cost(a) <= cost(b) ? a : b));
    const card = el("article", undefined, "flight"),
      main = el("div", undefined, "flight-main"),
      carrier = el("div", undefined, "carrier");
    carrier.append(
      el("div", [...new Set(item.segments.map((s) => s.airline))].join(" + ")),
      el(
        "div",
        item.segments.map((s) => s.flightNumber).join(" · "),
        "flight-sub",
      ),
    );
    const route = el("div"),
      line = el("div", undefined, "route-line");
    for (const [i, s] of [first, last].entries()) {
      if (i) line.append(el("span"));
      const endpoint = el("div");
      endpoint.append(
        el(
          "div",
          (i ? s.arrivalLocal : s.departureLocal).slice(11, 16),
          "time",
        ),
        el("div", i ? s.destination : s.origin, "flight-sub"),
      );
      line.append(endpoint);
    }
    route.append(
      line,
      el(
        "div",
        item.segments.length === 1
          ? "Nonstop"
          : item.segments.length -
              1 +
              " stop" +
              (item.segments.length === 2 ? "" : "s"),
        "route-stops",
      ),
    );
    const info = el("div", cabinNames[result.query.cabin], "flight-sub");
    if (fare.seats !== undefined)
      info.append(el("div", fare.seats + " seats shown", "seat"));
    const price = el("div", undefined, "price");
    if (fare.pricingType === "miles_cash") {
      price.classList.add("miles-cash");
      if (fare.milesCash) {
        price.append(el("strong", number(fare.milesCash.miles) + " miles"),
          el("small", "Maximum-offset estimate"),
          el("div", "+ " + money(fare.milesCash.remainingCash), "cash"));
      } else {
        price.append(el("strong", money(fare.cashPrice)),
          el("small", "Full cash fare"),
          el("div", fare.milesCashEligible === false ? "Not eligible for Miles & Cash" : "Miles conversion unavailable", "cash"));
      }
    } else price.append(
      el("strong", number(fare.points)),
      el("small", " " + fare.program),
      el("div", "+ " + fare.taxes.amount + " " + fare.taxes.currency, "cash"),
    );
    main.append(carrier, route, info, price);
    card.append(main);
    const details = el("details");
    details.append(
      el(
        "summary",
        "Flight details & " +
          item.fares.length +
          " fare option" +
          (item.fares.length === 1 ? "" : "s"),
      ),
    );
    const segments = el("div", undefined, "segments");
    for (const s of item.segments) {
      const seg = el("div", undefined, "segment");
      seg.append(
        el("strong", s.flightNumber + " · " + s.origin + " → " + s.destination),
        el(
          "div",
          s.departureLocal.replace("T", " ") +
            " → " +
            s.arrivalLocal.replace("T", " "),
        ),
        el(
          "div",
          "Aircraft: " +
            (s.aircraft || "Not provided") +
            (s.operatingAirline ? " · Operated by " + s.operatingAirline : ""),
        ),
      );
      segments.append(seg);
    }
    details.append(segments);
    for (const f of item.fares) {
      const row = el("div", undefined, "fare");
      row.append(
        el("span", f.name + (f.estimated ? " · estimate" : "")),
        el(
          "strong",
          f.pricingType === "miles_cash"
          ? (f.milesCash ? number(f.milesCash.miles) + " miles + " + money(f.milesCash.remainingCash)
            : money(f.cashPrice) + (f.milesCashEligible === false ? " cash · not eligible for Miles & Cash" : " cash · miles conversion unavailable"))
          : number(f.points) +
            " " +
            f.program +
            " + " +
            f.taxes.amount +
            " " +
            f.taxes.currency,
        ),
      );
      details.append(row);
    }
    card.append(details);
    flights.append(card);
  }
}
for (const id of ["sort", "stops", "airline"])
  $(id).addEventListener("change", () => result && renderFlights());
$("download").addEventListener("click", () => {
  if (!result) return;
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(result, null, 2)], { type: "application/json" }),
  );
  const a = el("a");
  a.href = url;
  a.download = `awards-${result.query.origin}-${result.query.destination}-${result.query.date}.json`;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
});
(async () => {
  const date = new Date();
  const local = (d) =>
    [
      d.getFullYear(),
      String(d.getMonth() + 1).padStart(2, "0"),
      String(d.getDate()).padStart(2, "0"),
    ].join("-");
  $("date").min = local(date);
  date.setDate(date.getDate() + 9);
  $("date").value = local(date);
  date.setFullYear(date.getFullYear() + 1);
  date.setDate(date.getDate() - 9);
  $("date").max = local(date);
  busy(false);
  try {
    config = await api("/api/config");
    $("provider").replaceChildren(
      ...config.providers.map(
        (p) => new Option(p.name + " · " + p.program, p.id),
      ),
    );
    if (config.providers.some(p => p.id === "eva")) $("provider").value = "eva";
    function providerChanged() {
      const p = config.providers.find((p) => p.id === $("provider").value);
      if (!p) return;
      $("submit").textContent = p.id === "eva" ? "Find EVA flights ↗" : "Find award flights ↗";
      $("sort").options[0].textContent = p.id === "eva" ? "Lowest cash fare" : "Lowest points";
      $("cabin").replaceChildren(
        ...p.cabins.map((c) => new Option(cabinNames[c] || c, c)),
      );
      $("adults").replaceChildren(
        ...Array.from(
          { length: p.maxAdults },
          (_, i) => new Option(i + 1 + " adult" + (i ? "s" : ""), i + 1),
        ),
      );
    }
    $("provider").addEventListener("change", providerChanged);
    providerChanged();
    $("setup").hidden = config.ready;
    $("connection").textContent = config.ready
      ? config.proxyEnabled
        ? "● Sticky proxy configured"
        : "● Direct connection"
      : "○ Server setup required";
    busy(false);
    currentJob = /^[a-f0-9]{32}$/.test(location.hash.slice(1))
      ? location.hash.slice(1)
      : sessionStorage.getItem("awarddesk.job");
    if (currentJob) {
      $("empty").hidden = true;
      busy(true);
      poll();
    }
  } catch (e) {
    status("offline", "Local server unavailable", e.message, true);
  }
})();

