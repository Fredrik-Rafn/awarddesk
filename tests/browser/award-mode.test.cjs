const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const asset = (name) => fs.readFileSync(path.join(__dirname, "../../internal/web/assets", name), "utf8");

function harness(savedJob) {
  const nodes = new Map(), observers = [];
  const timeouts = new Map(), storage = new Map();
  let nextTimeout = 1, requestHandler;
  class Node {
    constructor(tag = "div", text = "", value) {
      this.tag = tag; this.children = []; this.events = {}; this.attributes = {};
      this.dataset = {}; this.style = {}; this.textContent = text; this._value = value || "";
      this.className = ""; this.isConnected = true;
      this.classList = {
        add: (...names) => this.className += " " + names.join(" "),
        contains: (name) => this.className.split(" ").includes(name),
        toggle() {},
      };
    }
    get options() { return this.children; }
    get label() { return this.textContent; }
    get value() { return this.tag === "select" ? this.children.find((c) => c.value === this._value)?.value ?? this.children[0]?.value ?? "" : this._value; }
    set value(v) { this._value = String(v); }
    get selectedIndex() { return this.children.findIndex((c) => c.value === this.value); }
    set selectedIndex(i) { this.value = this.children[i].value; }
    get selectedOptions() { return this.children.filter((c) => c.value === this.value); }
    get firstElementChild() { return this.children[0]; }
    append(...items) { items.forEach((n) => { n.parent = this; this.children.push(n); }); }
    replaceChildren(...items) { this.children = []; this.textContent = ""; this.append(...items); }
    before(n) { this.wrapper = n; }
    setAttribute(k, v) { this.attributes[k] = v; }
    getAttribute(k) { return this.attributes[k]; }
    removeAttribute(k) { delete this.attributes[k]; }
    addEventListener(k, fn) { (this.events[k] ||= []).push(fn); }
    dispatchEvent(event) { for (const fn of this.events[event.type] || []) fn(event); return true; }
    querySelectorAll() { return []; }
    querySelector() { return null; }
    getBoundingClientRect() { return { width: 250, left: 30, top: 30, bottom: 70 }; }
    scrollIntoView() {}
    focus() {}
    contains(n) { return this === n || this.children.some((c) => c.contains(n)); }
  }
  const selectIDs = ["search-mode", "provider", "cabin", "adults", "sort", "airline", "stops"];
  const get = (id) => {
    if (!nodes.has(id)) nodes.set(id, new Node(selectIDs.includes(id) ? "select" : "div"));
    return nodes.get(id);
  };
  const option = (label, value) => new Node("option", label, String(value));
  get("search-mode").append(option("Award tickets", "award"), option("Miles & Cash", ""));
  get("provider").append(option("EVA", "eva"));
  get("cabin").append(option("Economy", "economy"));
  get("adults").append(option("1 adult", "1"));
  get("sort").append(option("Lowest cash fare", "points"));
  get("airline").append(option("All airlines", "all"));
  get("stops").append(option("All flights", "all"));
  get("search-progress").append(new Node("span"));
  const document = new Node();
  document.body = new Node("body");
  document.getElementById = get;
  document.createElement = (tag) => new Node(tag);
  document.querySelectorAll = (selector) => selector.startsWith("select:")
    ? selectIDs.map(get).filter((n) => !n.dataset.customSelect)
    : selector === "input[type=date]" ? [get("date")] : [];
  const requests = [];
  const context = vm.createContext({
    document, window: new Node(), Option: function (label, value) { return option(label, value); },
    Event: class { constructor(type) { this.type = type; } },
    CustomEvent: class { constructor(type, init) { this.type = type; Object.assign(this, init); } },
    MutationObserver: class { constructor(fn) { observers.push(fn); } observe() {} disconnect() {} },
    setTimeout(fn) { const id = nextTimeout++; timeouts.set(id, fn); return id; },
    clearTimeout(id) { timeouts.delete(id); }, setInterval() {}, clearInterval() {},
    innerWidth: 1200, innerHeight: 800,
    location: { hash: savedJob ? "#" + "a".repeat(32) : "" },
    sessionStorage: { getItem: (k) => storage.get(k), setItem: (k, v) => storage.set(k, v), removeItem: (k) => storage.delete(k) },
    workspaceHTTP: { async request(url, options) {
      requests.push({ url, options });
      if (url === "/api/config") return { evaLoginReady: true, providers: [{ id: "eva", name: "EVA", program: "Infinity", cabins: ["economy", "premium_economy", "business"], maxAdults: 9, awardMaxAdults: 6 }] };
      if (requestHandler) return requestHandler(url, options);
      return savedJob || { id: "b".repeat(32), status: "running", progress: {} };
    } },
  });
  vm.runInContext(asset("app.js"), context);
  return { get, context, requests, timeouts, storage,
    setRequestHandler: (fn) => { requestHandler = fn; },
    run: (s) => vm.runInContext(s, context),
    flush: async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); },
    enhance: () => vm.runInContext(asset("components.js"), context),
    calendar: () => vm.runInContext(asset("calendar.js"), context),
    mutations: () => observers.forEach((fn) => fn()),
    text: (n) => n.textContent + n.children.map((c) => c.textContent + c.children.map(function walk(x) { return x.textContent + x.children.map(walk).join(" "); }).join(" ")).join(" "),
  };
}
const query = { provider: "eva", mode: "award", origin: "TPE", destination: "LHR", date: "2026-12-20", cabin: "business", adults: 2 };
const itinerary = (points, taxes) => ({ segments: [{ airline: "EVA", flightNumber: "BR67", origin: "TPE", destination: "LHR", departureLocal: "2026-12-20T08:00", arrivalLocal: "2026-12-20T20:00", cabin: "economy" }], fares: [{ name: "Award", points, program: "Infinity", availableFor: 2, taxes, milesCashEligible: false }] });

test("award tickets are the initial default; explicit Miles & Cash retains larger parties", async () => {
  const h = harness(); await h.flush();
  assert.equal(h.get("search-mode").value, "award");
  assert.equal(h.get("adults").options.length, 6);
  assert.equal(h.get("submit").textContent, "Find award flights ↗");
  assert.equal(h.get("sort").options[0].textContent, "Lowest miles");
  const html = asset("index.html");
  assert.match(html, /<option value="award" selected>Award tickets<\/option>\s*<option value="">Miles &amp; Cash<\/option>/);
  h.get("search-mode").value = "";
  h.get("search-mode").dispatchEvent({ type: "change" });
  assert.equal(h.get("adults").options.length, 9);
  assert.equal(h.get("submit").textContent, "Find EVA flights ↗");
  h.get("cabin").value = "business"; h.get("adults").value = "8";
  h.get("search-mode").value = "award";
  h.get("search-mode").dispatchEvent({ type: "change" });
  assert.equal(h.get("cabin").value, "business");
  assert.equal(h.get("adults").value, "6");
  assert.equal(h.get("adults").options.length, 6);
  assert.equal(h.get("sort").options[0].textContent, "Lowest miles");
  h.get("search-mode").value = ""; h.get("search-mode").dispatchEvent({ type: "change" });
  assert.equal(h.get("adults").value, "6");
  assert.equal(h.get("adults").options.length, 9);
});

test("enhanced mode selector commits with keyboard and follows busy state", async () => {
  const h = harness(); await h.flush(); h.enhance();
  const select = h.get("search-mode"), trigger = select.wrapper.children[1];
  const key = (key) => trigger.onkeydown({ key, preventDefault() {} });
  key("ArrowDown"); key("End"); key("Enter");
  assert.equal(select.value, "");
  assert.match(trigger.getAttribute("aria-label"), /Miles & Cash/);
  key("ArrowDown"); key("Home"); key("Enter");
  assert.equal(select.value, "award");
  assert.match(trigger.getAttribute("aria-label"), /Award tickets/);
  h.run("busy(true)"); h.mutations();
  assert.equal(trigger.disabled, true);
  assert.equal(h.get("submit").textContent, "Searching award flights…");
  h.run("busy(false)"); h.mutations();
  assert.equal(trigger.disabled, false);
});

test("submission sends the selected award or explicit Miles & Cash mode", async () => {
  for (const mode of ["", "award"]) {
    const h = harness(); await h.flush();
    h.get("origin").value = "tpe"; h.get("destination").value = "lhr";
    h.get("search-mode").value = mode;
    await h.get("search").events.submit[0]({ preventDefault() {} });
    const body = JSON.parse(h.requests.find((r) => r.options?.method === "POST").options.body);
    assert.equal(body.mode, mode); assert.equal(body.origin, "TPE");
  }
});

test("an initial search submits award mode without changing the selector", async () => {
  const h = harness(); await h.flush();
  h.get("origin").value = "TPE"; h.get("destination").value = "HKG";
  await h.get("search").events.submit[0]({ preventDefault() {} });
  const body = JSON.parse(h.requests.find((r) => r.options?.method === "POST").options.body);
  assert.equal(body.mode, "award");
});

test("restoring a historical query with omitted mode keeps Miles & Cash labels and party limit", async () => {
  const legacy = { ...query, adults: 7 };
  delete legacy.mode;
  const flight = itinerary(0);
  flight.fares = [{ name: "Basic", pricingType: "miles_cash", cashPrice: { amount: "12000", currency: "TWD" }, milesCash: { miles: 10000, remainingCash: { amount: "8500", currency: "TWD" } } }];
  const h = harness({ status: "completed", query: legacy, result: { query: legacy, complete: true, warnings: [], itineraries: [flight] } });
  await h.flush();
  assert.equal(h.get("search-mode").value, "");
  assert.equal(h.get("submit").textContent, "Find EVA flights ↗");
  assert.equal(h.get("sort").options[0].textContent, "Lowest cash fare");
  assert.equal(h.get("adults").options.length, 9);
  assert.equal(h.get("adults").value, "7");
  assert.equal(h.get("result-title").textContent, "1 flight option · Miles & Cash");
  assert.match(h.text(h.get("flights")), /10,000 miles/);
});

test("restored award jobs restore controls, mileage labels and unknown taxes without inventing seats", async () => {
  const h = harness({ status: "completed", query, result: { query, complete: true, warnings: [], itineraries: [itinerary(75000), itinerary(65000, { amount: "0", currency: "TWD" })] } });
  await h.flush();
  assert.equal(h.get("search-mode").value, "award");
  assert.equal(h.get("adults").value, "2");
  assert.equal(h.get("cabin").value, "business");
  assert.equal(h.get("submit").textContent, "Find award flights ↗");
  assert.equal(h.get("sort").options[0].textContent, "Lowest miles");
  assert.equal(h.get("result-title").textContent, "2 award itineraries");
  const text = h.text(h.get("flights"));
  assert.match(text, /65,000/); assert.match(text, /Taxes \/ fees not quoted/);
  assert.match(text, /\+ 0 TWD/); assert.match(text, /Available for 2 adults/);
  assert.match(text, /Cabin: Economy/); assert.doesNotMatch(text, /seats shown/);
  assert.match(h.text(h.get("flights").children[0]), /65,000/);
  h.get("search-mode").value = ""; h.get("search-mode").dispatchEvent({ type: "change" });
  assert.equal(h.get("sort").options[0].textContent, "Lowest miles");
});

test("a running restored award search uses award progress and disables mode changes", async () => {
  const h = harness({ status: "running", query, progress: { total: 3, completed: 1 } });
  await h.flush();
  assert.equal(h.get("search-mode").value, "award");
  assert.equal(h.get("search-mode").disabled, true);
  assert.equal(h.get("submit").textContent, "Searching award flights…");
  assert.equal(h.get("status-message").textContent, "Checking award mileage quotes");
});

test("tax estimate progress keeps available award previews visible and labels the current work", async () => {
  for (const total of [0, 2]) {
    const h = harness({ status: "running", query, updatedAt: "tax-stage", progress: { stage: "award tax estimates", total, completed: 0 }, result: { query, complete: true, warnings: [], itineraries: [itinerary(75000)] } });
    await h.flush();
    assert.equal(h.get("status-message").textContent, "Retrieving tax estimates");
    assert.match(h.get("status-detail").textContent, /Checking EVA cash fares for tax estimates\. Actual award taxes remain unverified/);
    assert.equal(h.get("results").hidden, false);
    assert.equal(h.get("search-skeleton").hidden, true);
    assert.equal(h.get("flights").children.length, 1);
    assert.match(h.text(h.get("flights")), /75,000/);
    if (total) assert.equal(h.get("search-progress").getAttribute("aria-valuetext"), "0 of 2 tax estimates checked");
  }
});

test("failed award searches emphasize validated partial results and missing quotes honestly", async () => {
  const h = harness({ status: "failed", query, result: { query, complete: false, warnings: [], itineraries: [itinerary(75000)] } });
  await h.flush();
  assert.equal(h.get("status-message").textContent, "Some award results are available");
  assert.match(h.get("status-detail").textContent, /incomplete.*additional flights or mileage quotes may be missing/);
  assert.match(h.get("result-meta").textContent, /Partial results/);
  assert.doesNotMatch(h.get("result-meta").textContent, /Available quotes retrieved/);
  assert.equal(h.get("flights").children.length, 1);
  assert.equal(h.get("submit").disabled, false);
});

test("Miles & Cash results retain eligibility filtering and cash labels", async () => {
  const h = harness(); await h.flush();
  h.context.fixture = { query: { ...query, mode: "" }, complete: true, warnings: [], itineraries: [itinerary(75000), { ...itinerary(0), fares: [{ name: "Basic", pricingType: "miles_cash", cashPrice: { amount: "12000", currency: "TWD" }, milesCash: { miles: 10000, remainingCash: { amount: "8500", currency: "TWD" } } }] }] };
  h.run("result = fixture; showResults()");
  assert.equal(h.get("result-title").textContent, "1 flight option · Miles & Cash");
  assert.equal(h.get("sort").options[0].textContent, "Lowest cash fare");
  assert.match(h.text(h.get("flights")), /10,000 miles/);
  assert.match(h.text(h.get("flights")), /8,500 TWD/);
});

test("restoring a job synchronizes the enhanced calendar date and uses neutral program labels", async () => {
  const restoredQuery = { ...query, date: "2026-11-24" };
  const h = harness({ status: "completed", query: restoredQuery, result: { query: restoredQuery, complete: true, warnings: [], itineraries: [itinerary(75000)] } });

  h.calendar();
  const trigger = h.get("date").wrapper.children[1];
  await h.flush();
  assert.equal(h.get("date").value, "2026-11-24");
  assert.equal(trigger.children[0].textContent, "24 Nov 2026");
  assert.equal(trigger.getAttribute("aria-label"), "Date, 24 Nov 2026");
  assert.equal(h.get("provider").options[0].textContent, "EVA Air · Infinity");
  assert.equal(h.get("result-title").textContent, "1 award itinerary");
});

test("historical generic award disclaimers are hidden while actionable warnings and unknown taxes remain", async () => {
  const generic = "Award availability was checked for the requested party. Mileage is the per-person quote multiplied by adults; taxes and fees were not quoted. Availability is not a confirmed reservation.";
  const actionable = ["Airport change required in Taipei.", "Search incomplete: some mileage quotes are missing."];
  const h = harness({ status: "completed", query, result: { query, complete: false, warnings: [generic, ...actionable], itineraries: [itinerary(75000)] } });
  await h.flush();
  assert.deepEqual(Array.from(h.get("warnings").children, (n) => n.textContent), actionable);
  assert.equal(h.get("warnings").hidden, false);
  assert.match(h.text(h.get("flights")), /Taxes \/ fees not quoted/);
  h.run("result.warnings = result.warnings.slice(0, 1); showResults()");
  assert.equal(h.get("warnings").children.length, 0);
  assert.equal(h.get("warnings").hidden, true);
});

const taxEstimate = {
  total: { amount: "6094", currency: "TWD" },
  source: "eva_cash_fare",
  quotedAt: "2026-10-23T20:48:04Z",
  components: [
    { code: "YQAC", name: "Airline Fuel Surcharge", amount: { amount: "2444", currency: "TWD" } },
    { code: "YRVB", name: "Booking Service Charge", amount: { amount: "2150", currency: "TWD" } },
    { code: "TWAE", name: "Taiwan Airport Service Charge", amount: { amount: "1500", currency: "TWD" } },
  ],
};

test("award tax estimates show the supplied party total, itemized fees and cash source without changing miles", async () => {
  const h = harness(); await h.flush();
  const flight = itinerary(75000);
  flight.fares[0].taxEstimate = taxEstimate;
  const before = JSON.stringify(flight.fares[0]);
  h.context.fixture = { query, complete: true, warnings: [], itineraries: [flight] };
  h.run("result = fixture; showResults()");
  const text = h.text(h.get("flights"));
  assert.match(text, /≈ TWD 6,094 taxes & fees \(estimate\)/);
  assert.doesNotMatch(text, /12,188|Taxes \/ fees not quoted/);
  assert.match(text, /Airline Fuel Surcharge \(YQAC\).*TWD 2,444/);
  assert.match(text, /Booking Service Charge \(YRVB\).*TWD 2,150/);
  assert.match(text, /Taiwan Airport Service Charge \(TWAE\).*TWD 1,500/);
  assert.match(text, /Based on EVA selected cash fare pricing for these flights\. Actual award taxes are unverified\. Total for 2 adults\./);
  assert.match(text, /75,000/);
  assert.equal(JSON.stringify(flight.fares[0]), before);
  assert.equal(h.get("warnings").hidden, true);
});

test("actual award taxes including zero take priority over a cash-derived estimate", async () => {
  for (const amount of ["1200", "0"]) {
    const h = harness(); await h.flush();
    const flight = itinerary(75000, { amount, currency: "TWD" });
    flight.fares[0].taxEstimate = taxEstimate;
    h.context.fixture = { query, complete: true, warnings: [], itineraries: [flight] };
    h.run("result = fixture; showResults()");
    const text = h.text(h.get("flights"));
    assert.match(text, amount === "0" ? /\+ 0 TWD/ : /\+ 1,200 TWD/);
    assert.doesNotMatch(text, /6,094|unverified|YQAC|Estimated taxes|≈/);
  }
});

test("zero tax estimates are explicit while absent or unsupported estimates stay unquoted", async () => {
  const h = harness(); await h.flush();
  const flight = itinerary(75000);
  flight.fares[0].taxEstimate = { ...taxEstimate, total: { amount: "0", currency: "TWD" }, components: [] };
  h.context.fixture = { query: { ...query, adults: 1 }, complete: true, warnings: [], itineraries: [flight] };
  h.run("result = fixture; showResults()");
  assert.match(h.text(h.get("flights")), /≈ TWD 0 taxes & fees \(estimate\)/);
  assert.match(h.text(h.get("flights")), /Total for 1 adult\./);
  for (const estimate of [undefined, { ...taxEstimate, source: "unknown" }, { ...taxEstimate, total: { amount: null, currency: "TWD" } }]) {
    flight.fares[0].taxEstimate = estimate;
    h.run("showResults()");
    assert.match(h.text(h.get("flights")), /Taxes \/ fees not quoted/);
    assert.doesNotMatch(h.text(h.get("flights")), /≈|unverified/);
  }
});

test("Miles & Cash pricing ignores award tax estimates", async () => {
  const h = harness(); await h.flush();
  const flight = itinerary(0);
  flight.fares = [{ name: "Basic", pricingType: "miles_cash", cashPrice: { amount: "12000", currency: "TWD" }, milesCash: { miles: 10000, remainingCash: { amount: "8500", currency: "TWD" } }, taxEstimate }];
  h.context.fixture = { query: { ...query, mode: "" }, complete: true, warnings: [], itineraries: [flight] };
  h.run("result = fixture; showResults()");
  const text = h.text(h.get("flights"));
  assert.match(text, /10,000 miles/);
  assert.match(text, /8,500 TWD/);
  assert.doesNotMatch(text, /6,094|unverified|YQAC|Estimated taxes|≈/);
});

const taxUSD = {
  amount: "186.98", currency: "USD", rate: "32.592",
  asOf: "2026-10-23T00:00:00Z", source: "https://www.exchangerate-api.com",
};
async function renderTaxFare(taxes, estimate, taxesUsd) {
  const h = harness(); await h.flush();
  const flight = itinerary(75000, taxes);
  flight.fares[0].taxEstimate = estimate;
  flight.fares[0].taxesUsd = taxesUsd;
  h.context.fixture = { query, complete: true, warnings: [], itineraries: [flight] };
  h.run("result = fixture; showResults()");
  return h;
}

test("USD estimates are primary while original totals, components and attributed rate remain in details", async () => {
  const h = await renderTaxFare(undefined, { ...taxEstimate, usd: taxUSD });
  const card = h.get("flights").children[0];
  const main = h.text(card.children[0]);
  assert.match(main, /≈ US\$186\.98 taxes & fees \(estimate\)/);
  assert.doesNotMatch(main, /6,094|conversion unavailable/);
  const details = h.text(card.children[1]);
  assert.match(details, /Original total: TWD 6,094/);
  assert.match(details, /TWD 2,444/);
  assert.match(details, /Actual award taxes are unverified/);
  assert.match(details, /USD conversion: 1 USD = 32\.592 TWD · Rate date: 2026-10-23/);
  assert.match(details, /Rates by ExchangeRate-API/);
  const links = [];
  function findLinks(n) { if (n.tag === "a") links.push(n); n.children.forEach(findLinks); }
  findLinks(card.children[1]);
  assert.equal(links.length, 1);
  assert.equal(links[0].href, "https://www.exchangerate-api.com");
});

test("cash availability tax estimates show USD with distinct provenance and no invented breakdown", async () => {
  const h = await renderTaxFare(undefined, { ...taxEstimate, source: "eva_cash_availability", usd: taxUSD, components: [] });
  const text = h.text(h.get("flights"));
  assert.match(text, /≈ US\$186\.98 taxes & fees \(estimate\)/);
  assert.match(text, /Based on EVA cash availability for these flights\. Actual award taxes are unverified\. Total for 2 adults\./);
  assert.match(text, /Original total: TWD 6,094/);
  assert.match(text, /Rates by ExchangeRate-API/);
  assert.doesNotMatch(text, /selected cash fare|YQAC|YRVB|TWAE|Taxes \/ fees not quoted/);
});

test("an unknown tax estimate source remains unquoted even with a valid USD conversion", async () => {
  const h = await renderTaxFare(undefined, { ...taxEstimate, source: "unknown_cash_source", usd: taxUSD });
  const text = h.text(h.get("flights"));
  assert.match(text, /Taxes \/ fees not quoted/);
  assert.doesNotMatch(text, /US\$|Original total|unverified|Rates by ExchangeRate-API/);
});

test("actual tax USD uses its corresponding conversion and zero still overrides an estimate", async () => {
  for (const amount of ["1200", "0"]) {
    const usd = { ...taxUSD, amount: amount === "0" ? "0.00" : "37.50", rate: "32" };
    const h = await renderTaxFare({ amount, currency: "TWD" }, { ...taxEstimate, usd: taxUSD }, usd);
    const text = h.text(h.get("flights"));
    assert.match(text, amount === "0" ? /\+ ≈ US\$0\.00/ : /\+ ≈ US\$37\.50/);
    assert.match(text, amount === "0" ? /Original total: TWD 0/ : /Original total: TWD 1,200/);
    assert.doesNotMatch(text, /186\.98|unverified|YQAC|\(estimate\)/);
  }
});

test("invalid or unknown USD conversions keep original taxes and explain unavailable conversion", async () => {
  for (const usd of [undefined,
    { ...taxUSD, source: "https://unknown.example" },
    { ...taxUSD, currency: "EUR" },
    { ...taxUSD, amount: "NaN" },
    { ...taxUSD, amount: "-1" },
    { ...taxUSD, rate: "0" },
    { ...taxUSD, rate: "Infinity" },
    { ...taxUSD, asOf: "invalid-date" },
    { ...taxUSD, asOf: undefined },
    { ...taxUSD, amount: "999.99" },
  ]) {
    const h = await renderTaxFare(undefined, { ...taxEstimate, usd });
    const text = h.text(h.get("flights"));
    assert.match(text, /≈ TWD 6,094 taxes & fees \(estimate\) · USD conversion unavailable/);
    assert.doesNotMatch(text, /US\$|Rates by ExchangeRate-API/);
  }

  const actual = await renderTaxFare({ amount: "0", currency: "TWD" }, { ...taxEstimate, usd: taxUSD });
  assert.match(actual.text(actual.get("flights")), /\+ 0 TWD · USD conversion unavailable/);
});

test("native USD amounts need no conversion or attribution", async () => {
  const actual = await renderTaxFare({ amount: "0", currency: "USD" }, { ...taxEstimate, usd: taxUSD });
  const estimated = await renderTaxFare(undefined, { ...taxEstimate, total: { amount: "93.49", currency: "USD" }, usd: undefined, components: [] });
  assert.match(actual.text(actual.get("flights")), /\+ US\$0\.00/);
  assert.match(estimated.text(estimated.get("flights")), /≈ US\$93\.49 taxes & fees \(estimate\)/);
  for (const h of [actual, estimated])
    assert.doesNotMatch(h.text(h.get("flights")), /conversion unavailable|Rates by ExchangeRate-API/);
});

test("Miles & Cash totals ignore tax USD conversion fields", async () => {
  const h = harness(); await h.flush();
  const flight = itinerary(0);
  flight.fares = [{ name: "Basic", pricingType: "miles_cash", cashPrice: { amount: "12000", currency: "TWD" }, milesCash: { miles: 10000, remainingCash: { amount: "8500", currency: "TWD" } }, taxes: { amount: "6094", currency: "TWD" }, taxesUsd: taxUSD, taxEstimate: { ...taxEstimate, usd: taxUSD } }];
  h.context.fixture = { query: { ...query, mode: "" }, complete: true, warnings: [], itineraries: [flight] };
  h.run("result = fixture; showResults()");
  const text = h.text(h.get("flights"));
  assert.match(text, /10,000 miles/);
  assert.match(text, /8,500 TWD/);
  assert.doesNotMatch(text, /US\$|Rates by ExchangeRate-API|USD conversion/);
});

test("interrupted jobs stop polling and progress, restore retry controls and retain available results", async () => {
  for (const hasResults of [false, true]) {
    const h = harness({ status: "interrupted", query, result: hasResults ? { query, complete: false, warnings: [], itineraries: [itinerary(75000)] } : undefined });
    h.storage.set("awarddesk.job", "a".repeat(32));
    await h.flush();
    assert.equal(h.run("currentJob"), null);
    assert.equal(h.run("searchBusy"), false);
    assert.equal(h.get("submit").disabled, false);
    assert.equal(h.get("search-mode").disabled, false);
    assert.equal(h.get("cancel").hidden, true);
    assert.equal(h.get("search-progress").hidden, true);
    assert.equal(h.get("search-progress").getAttribute("aria-valuenow"), undefined);
    assert.equal(h.get("search-progress").firstElementChild.style.width, "");
    assert.equal(h.get("search-elapsed").textContent, "");
    assert.equal(h.storage.has("awarddesk.job"), false);
    assert.equal(h.timeouts.size, 0);
    assert.equal(h.get("status-message").textContent, "Search interrupted");
    assert.match(h.get("status-detail").textContent, /server restarted.*Start a new search to try again/);
    if (hasResults) {
      assert.equal(h.get("results").hidden, false);
      assert.match(h.text(h.get("flights")), /75,000/);
      assert.match(h.get("result-meta").textContent, /Partial results/);
    }
    const before = h.requests.length;
    await h.run("poll()");
    assert.equal(h.requests.length, before);
  }
});

test("stop following detaches locally, sends no cancellation and allows a different query", async () => {
  const h = harness({ status: "running", query, updatedAt: "preview", progress: {}, result: { query, complete: false, warnings: [], itineraries: [itinerary(75000)] } });
  await h.flush();
  h.storage.set("awarddesk.job", "a".repeat(32));
  assert.equal(h.get("cancel").textContent, "Stop following search");
  assert.equal(h.timeouts.size, 1);
  const before = h.requests.length;
  h.get("cancel").events.click[0]();
  assert.equal(h.requests.length, before);
  assert.equal(h.timeouts.size, 0);
  assert.equal(h.context.location.hash, "");
  assert.equal(h.storage.has("awarddesk.job"), false);
  assert.equal(h.get("submit").disabled, false);
  assert.equal(h.get("status-message").textContent, "Stopped following this search");
  assert.match(h.get("status-detail").textContent, /shared search can continue for others/);
  assert.equal(h.get("results").hidden, false);
  await h.run("poll()");
  assert.equal(h.requests.length, before);
  h.setRequestHandler(() => ({ id: "b".repeat(32), status: "running", query: { ...query, destination: "SFO" }, progress: {} }));
  h.get("destination").value = "SFO";
  await h.get("search").events.submit[0]({ preventDefault() {} });
  await h.flush();
  assert.equal(h.run("currentJob"), "b".repeat(32));
  assert.equal(JSON.parse(h.requests.find((r) => r.options?.method === "POST").options.body).destination, "SFO");
  assert.equal(h.requests.some((r) => r.options?.method === "DELETE"), false);
});

test("late responses from a detached poll cannot stop a new subscription to the same shared job", async () => {
  const h = harness({ status: "running", query, progress: {} });
  await h.flush();
  let resolveOld;
  h.setRequestHandler(() => new Promise((resolve) => { resolveOld = resolve; }));
  const oldPoll = h.run("poll()");
  h.get("cancel").events.click[0]();
  h.setRequestHandler(() => ({ id: "a".repeat(32), status: "running", query, progress: { stage: "award tax estimates" } }));
  await h.get("search").events.submit[0]({ preventDefault() {} });
  await h.flush();
  resolveOld({ status: "interrupted", query });
  await oldPoll;
  assert.equal(h.run("currentJob"), "a".repeat(32));
  assert.equal(h.run("searchBusy"), true);
  assert.equal(h.get("status-message").textContent, "Retrieving tax estimates");
  assert.equal(h.get("submit").disabled, true);
});

test("detaching before search creation returns ignores its late success or failure", async () => {
  for (const rejectRequest of [false, true]) {
    const h = harness(); await h.flush();
    let settle;
    h.setRequestHandler(() => new Promise((resolve, reject) => { settle = rejectRequest ? reject : resolve; }));
    const creating = h.get("search").events.submit[0]({ preventDefault() {} });
    h.get("cancel").events.click[0]();
    settle(rejectRequest ? new Error("late failure") : { id: "b".repeat(32), status: "running", progress: {} });
    await creating;
    assert.equal(h.run("currentJob"), null);
    assert.equal(h.run("searchBusy"), false);
    assert.equal(h.context.location.hash, "");
    assert.equal(h.get("status-message").textContent, "Stopped following this search");
    assert.equal(h.requests.filter((r) => r.url !== "/api/config").length, 1);
    assert.equal(h.requests.some((r) => r.options?.method === "DELETE"), false);
  }
});
