const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");
const path = require("node:path");
const source = fs.readFileSync(
  path.join(__dirname, "../../internal/web/assets/admin-registration.js"),
  "utf8",
);
function harness() {
  const nodes = new Map(),
    events = {};
  let records = [
      {
        id: "batch-id",
        email: "fixture@example.test",
        status: "running",
        stage: "verification",
        message: "Waiting for email",
        updatedAt: "now",
      },
    ],
    pending;
  const make = () => ({
    children: [],
    textContent: "",
    value: "fixture",
    checked: true,
    append(...items) {
      this.children.push(...items);
    },
    replaceChildren(...items) {
      this.children = items;
      this.textContent = "";
    },
    setAttribute() {},
  });
  const get = (id) => {
    if (!nodes.has(id)) nodes.set(id, make());
    return nodes.get(id);
  };
  const context = vm.createContext({
    $: get,
    polling: true,
    currentView: () => "accounts",
    setInterval() {},
    window: {
      addEventListener(name, fn) {
        events[name] = fn;
      },
    },
    el: (tag, text) => ({ ...make(), tag, textContent: text }),
    cell: (...text) => text,
    table: (headers, rows) => ({ headers, rows }),
    date: (v) => v,
    renderPagination() {},
    message() {},
    refresh: async () => {},
    confirmAction: async () => true,
    api: async (url, options) => {
      if (options?.method === "POST")
        return { added: 1, skipped: 0, ids: ["batch-id"] };
      if (pending) return pending;
      return { items: records };
    },
  });
  vm.runInContext(source, context);
  return {
    get,
    events,
    context,
    setRecords: (v) => (records = v),
    setPending: (v) => (pending = v),
    run: (s) => vm.runInContext(s, context),
    submit: () => get("registration-form").onsubmit({ preventDefault() {} }),
  };
}
test("only the submitted batch appears; terminal rows disappear", async () => {
  const h = harness();
  await h.run("refreshRegistrations()");
  assert.equal(h.get("registration-list").children.length, 0);
  await h.submit();
  assert.equal(h.get("registration-list").children[1].rows.length, 1);
  h.setRecords([{ id: "batch-id", status: "ready" }]);
  await h.run("refreshRegistrations()");
  assert.equal(h.get("registration-list").children.length, 0);
  assert.match(h.get("registration-message").textContent, /accounts list/);
});
test("switching sections clears progress and a late response cannot restore it", async () => {
  const h = harness();
  await h.submit();
  let resolve;
  h.setPending(new Promise((r) => (resolve = r)));
  const refresh = h.run("refreshRegistrations()");
  h.events.hashchange();
  resolve({ items: [{ id: "batch-id", status: "running" }] });
  await refresh;
  assert.equal(h.get("registration-list").children.length, 0);
  assert.equal(h.get("registration-total").textContent, "");
});
test("refresh creates a clean page session rather than reloading old progress", async () => {
  const old = harness();
  await old.submit();
  assert.ok(old.get("registration-list").children.length);
  const fresh = harness();
  await fresh.run("refreshRegistrations()");
  assert.equal(fresh.get("registration-list").children.length, 0);
});
