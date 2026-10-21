"use strict";
(() => {
  const dayKey = (d) =>
    `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, "0")}-${String(d.getUTCDate()).padStart(2, "0")}`;
  const parse = (s) =>
    /^\d{4}-\d{2}-\d{2}$/.test(s) ? new Date(s + "T12:00:00Z") : null;
  const label = (d) =>
    new Intl.DateTimeFormat("en-GB", {
      day: "numeric",
      month: "short",
      year: "numeric",
      timeZone: "UTC",
    }).format(d);
  let opened = null;
  for (const input of document.querySelectorAll("input[type=date]")) {
    const wrap = document.createElement("div");
    wrap.className = "custom-date";
    input.before(wrap);
    wrap.append(input);
    input.classList.add("native-date");
    input.tabIndex = -1;
    input.setAttribute("aria-hidden", "true");
    const trigger = document.createElement("button");
    trigger.type = "button";
    trigger.className = "date-trigger";
    trigger.setAttribute("aria-haspopup", "dialog");
    trigger.setAttribute("aria-expanded", "false");
    const text = document.createElement("span");
    trigger.append(text);
    const icon = document.createElement("span");
    icon.className = "calendar-icon";
    icon.setAttribute("aria-hidden", "true");
    icon.innerHTML =
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><rect x="4" y="5" width="16" height="16" rx="3"/><path d="M8 3v4m8-4v4M4 11h16"/></svg>';
    trigger.append(icon);
    wrap.append(trigger);
    const popup = document.createElement("div");
    popup.className = "calendar-popup";
    popup.id = input.id + "-calendar";
    popup.hidden = true;
    popup.setAttribute("role", "dialog");
    popup.setAttribute("aria-label", "Choose departure date");
    trigger.setAttribute("aria-controls", popup.id);
    document.body.append(popup);
    let shown, focused;
    function sync() {
      text.textContent = parse(input.value)
        ? label(parse(input.value))
        : "Choose date";
      trigger.setAttribute(
        "aria-label",
        (input.labels?.[0]?.textContent.trim() || "Date") +
          ", " +
          text.textContent,
      );
      trigger.disabled = input.disabled;
    }
    function allowed(d) {
      const key = dayKey(d);
      return (
        (!input.min || key >= input.min) && (!input.max || key <= input.max)
      );
    }
    function close(returnFocus = false) {
      popup.hidden = true;
      trigger.setAttribute("aria-expanded", "false");
      if (opened === control) opened = null;
      if (returnFocus) trigger.focus();
    }
    function place() {
      const r = trigger.getBoundingClientRect(),
        width = Math.min(326, innerWidth - 24);
      popup.style.width = width + "px";
      popup.style.left =
        Math.max(12, Math.min(r.left, innerWidth - width - 12)) + "px";
      if (innerHeight - r.bottom < 390 && r.top > 390) {
        popup.style.top = "auto";
        popup.style.bottom = innerHeight - r.top + 8 + "px";
      } else {
        popup.style.bottom = "auto";
        popup.style.top = r.bottom + 8 + "px";
      }
      popup.style.maxHeight = Math.min(420, innerHeight - 24) + "px";
    }
    function choose(d) {
      input.value = dayKey(d);
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
      sync();
      close(true);
    }
    function monthButton(delta) {
      const b = document.createElement("button");
      b.type = "button";
      b.className = "calendar-nav";
      b.textContent = delta < 0 ? "‹" : "›";
      b.setAttribute("aria-label", delta < 0 ? "Previous month" : "Next month");
      const next = new Date(
          Date.UTC(shown.getUTCFullYear(), shown.getUTCMonth() + delta, 1, 12),
        ),
        last = new Date(
          Date.UTC(next.getUTCFullYear(), next.getUTCMonth() + 1, 0, 12),
        );
      b.disabled = !!(
        (input.min && dayKey(last) < input.min) ||
        (input.max && dayKey(next) > input.max)
      );
      b.onclick = () => {
        shown = next;
        focused = new Date(next);
        if (!allowed(focused)) focused = parse(input.min) || focused;
        render(true);
      };
      return b;
    }
    function render(moveFocus = false) {
      popup.replaceChildren();
      const header = document.createElement("div");
      header.className = "calendar-header";
      const month = document.createElement("strong");
      month.textContent = new Intl.DateTimeFormat("en-GB", {
        month: "long",
        year: "numeric",
        timeZone: "UTC",
      }).format(shown);
      month.setAttribute("aria-live", "polite");
      header.append(monthButton(-1), month, monthButton(1));
      popup.append(header);
      const grid = document.createElement("div");
      grid.className = "calendar-grid";
      grid.setAttribute("role", "grid");
      grid.setAttribute("aria-label", month.textContent);
      const weekdays = document.createElement("div");
      weekdays.className = "calendar-week";
      weekdays.setAttribute("role", "row");
      for (const name of ["Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"]) {
        const e = document.createElement("span");
        e.textContent = name;
        e.setAttribute("role", "columnheader");
        weekdays.append(e);
      }
      grid.append(weekdays);
      const first = new Date(
        Date.UTC(shown.getUTCFullYear(), shown.getUTCMonth(), 1, 12),
      );
      const start = new Date(first);
      start.setUTCDate(1 - ((first.getUTCDay() + 6) % 7));
      for (let week = 0; week < 6; week++) {
        const row = document.createElement("div");
        row.className = "calendar-week";
        row.setAttribute("role", "row");
        for (let day = 0; day < 7; day++) {
          const d = new Date(start);
          d.setUTCDate(start.getUTCDate() + week * 7 + day);
          const b = document.createElement("button");
          b.type = "button";
          b.textContent = d.getUTCDate();
          b.className =
            "calendar-day" +
            (d.getUTCMonth() !== shown.getUTCMonth() ? " outside-month" : "");
          b.dataset.day = dayKey(d);
          b.setAttribute("role", "gridcell");
          b.setAttribute("aria-label", label(d));
          b.setAttribute("aria-selected", String(input.value === dayKey(d)));
          b.tabIndex = dayKey(d) === dayKey(focused) ? 0 : -1;
          b.disabled = !allowed(d);
          const today = new Date();
          if (
            dayKey(d) ===
            `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, "0")}-${String(today.getDate()).padStart(2, "0")}`
          )
            b.setAttribute("aria-current", "date");
          b.onclick = () => choose(d);
          b.onkeydown = (e) => {
            const delta = {
              ArrowLeft: -1,
              ArrowRight: 1,
              ArrowUp: -7,
              ArrowDown: 7,
            }[e.key];
            let next = new Date(d);
            if (delta != null) next.setUTCDate(d.getUTCDate() + delta);
            else if (e.key === "Home")
              next.setUTCDate(d.getUTCDate() - ((d.getUTCDay() + 6) % 7));
            else if (e.key === "End")
              next.setUTCDate(d.getUTCDate() + 6 - ((d.getUTCDay() + 6) % 7));
            else if (e.key === "PageUp" || e.key === "PageDown") {
              next.setUTCDate(1);
              next.setUTCMonth(
                next.getUTCMonth() + (e.key === "PageUp" ? -1 : 1),
              );
            } else return;
            e.preventDefault();
            if (allowed(next)) {
              focused = next;
              shown = new Date(
                Date.UTC(next.getUTCFullYear(), next.getUTCMonth(), 1, 12),
              );
              render(true);
            }
          };
          row.append(b);
        }
        grid.append(row);
      }
      popup.append(grid);
      const note = document.createElement("p");
      note.className = "calendar-note";
      note.textContent = "One way · Departure date";
      popup.append(note);
      if (moveFocus)
        popup.querySelector('[data-day="' + dayKey(focused) + '"]')?.focus();
    }
    function open() {
      if (input.disabled) return;
      document.dispatchEvent(
        new CustomEvent("awarddesk:popover", { detail: trigger }),
      );
      opened?.close();
      opened = control;
      focused = parse(input.value) || parse(input.min) || new Date();
      if (!allowed(focused)) focused = parse(input.min) || focused;
      shown = new Date(
        Date.UTC(focused.getUTCFullYear(), focused.getUTCMonth(), 1, 12),
      );
      popup.hidden = false;
      trigger.setAttribute("aria-expanded", "true");
      place();
      render(true);
    }
    const control = { close, wrap, popup };
    document.addEventListener("awarddesk:popover", (event) => {
      if (event.detail !== trigger) close();
    });
    sync();
    trigger.onclick = () => (opened === control ? close() : open());
    trigger.onkeydown = (e) => {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        open();
      }
    };
    input.addEventListener("change", sync);
    input.addEventListener("focus", () => trigger.focus());
    new MutationObserver(sync).observe(input, { attributes: true });
    popup.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close(true);
      }
    });
    popup.addEventListener("focusout", (e) => {
      if (
        e.relatedTarget &&
        !popup.contains(e.relatedTarget) &&
        !wrap.contains(e.relatedTarget)
      )
        close();
    });
  }
  document.addEventListener("pointerdown", (e) => {
    if (
      opened &&
      !opened.wrap.contains(e.target) &&
      !opened.popup.contains(e.target)
    )
      opened.close();
  });
  window.addEventListener("resize", () => opened?.close());
  document.addEventListener(
    "scroll",
    (e) => {
      if (opened && !opened.popup.contains(e.target)) opened.close();
    },
    true,
  );
})();
