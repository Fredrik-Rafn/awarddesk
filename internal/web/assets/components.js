"use strict";
// Native selects remain the form/data source; the visible control is a keyboard
// accessible combobox. Menus live outside cards so they never get clipped.
(() => {
  let nextID = 0,
    opened = null;
  const all = new Set();
  function enhance(select) {
    if (select.dataset.customSelect || select.multiple || select.size > 1)
      return;
    select.dataset.customSelect = "true";
    const wrap = document.createElement("div");
    wrap.className =
      "custom-select" +
      (select.classList.contains("program") ? " program-select" : "");
    select.before(wrap);
    wrap.append(select);
    select.classList.add("native-select");
    select.tabIndex = -1;
    select.setAttribute("aria-hidden", "true");
    const button = document.createElement("button");
    button.type = "button";
    button.className = "select-trigger";
    button.setAttribute("role", "combobox");
    button.setAttribute("aria-expanded", "false");
    button.setAttribute("aria-haspopup", "listbox");
    const text = document.createElement("span"),
      chevron = document.createElement("span");
    chevron.className = "select-chevron";
    chevron.setAttribute("aria-hidden", "true");
    chevron.textContent = "⌄";
    button.append(text, chevron);
    wrap.append(button);
    const menu = document.createElement("div");
    menu.className = "select-menu";
    menu.id = "select-list-" + ++nextID;
    menu.setAttribute("role", "listbox");
    menu.hidden = true;
    document.body.append(menu);
    button.setAttribute("aria-controls", menu.id);
    let active = 0,
      search = "",
      searchTimer;
    function sync() {
      text.textContent = select.selectedOptions[0]?.label || "Choose an option";
      button.disabled = select.disabled;
      const name =
        select.getAttribute("aria-label") ||
        select.labels?.[0]?.childNodes[0]?.textContent?.trim() ||
        "Choose an option";
      button.setAttribute("aria-label", name + ", " + text.textContent);
      menu.setAttribute("aria-label", name);
      button.setAttribute("aria-required", String(select.required));
      if (opened === control) render();
    }
    function close() {
      menu.hidden = true;
      button.setAttribute("aria-expanded", "false");
      button.removeAttribute("aria-activedescendant");
      if (opened === control) opened = null;
    }
    function place() {
      const r = button.getBoundingClientRect(),
        width = Math.min(Math.max(r.width, 200), innerWidth - 24);
      menu.style.width = width + "px";
      menu.style.left =
        Math.max(12, Math.min(r.left, innerWidth - width - 12)) + "px";
      const below = innerHeight - r.bottom - 12;
      if (below < 160 && r.top > below) {
        menu.style.top = "auto";
        menu.style.bottom = innerHeight - r.top + 6 + "px";
        menu.style.maxHeight = Math.min(300, r.top - 18) + "px";
      } else {
        menu.style.bottom = "auto";
        menu.style.top = r.bottom + 6 + "px";
        menu.style.maxHeight = Math.max(100, Math.min(300, below)) + "px";
      }
    }
    function enabled() {
      return [...select.options]
        .map((o, i) => (!o.disabled && !o.hidden ? i : -1))
        .filter((i) => i >= 0);
    }
    function render() {
      menu.replaceChildren();
      [...select.options].forEach((option, index) => {
        if (option.hidden) return;
        const row = document.createElement("div");
        row.className =
          "select-option" + (index === active ? " highlighted" : "");
        row.id = menu.id + "-" + index;
        row.setAttribute("role", "option");
        row.setAttribute("aria-selected", String(option.selected));
        row.setAttribute("aria-disabled", String(option.disabled));
        row.textContent = option.label;
        const check = document.createElement("span");
        check.textContent = option.selected ? "✓" : "";
        check.setAttribute("aria-hidden", "true");
        row.append(check);
        row.onclick = () => {
          if (!option.disabled) commit(index);
        };
        menu.append(row);
      });
      button.setAttribute("aria-activedescendant", menu.id + "-" + active);
      menu.querySelector(".highlighted")?.scrollIntoView({ block: "nearest" });
    }
    function open() {
      if (select.disabled) return;
      document.dispatchEvent(
        new CustomEvent("awarddesk:popover", { detail: button }),
      );
      opened?.close();
      opened = control;
      active = select.selectedIndex;
      menu.hidden = false;
      button.setAttribute("aria-expanded", "true");
      place();
      render();
    }
    function commit(index) {
      select.selectedIndex = index;
      select.dispatchEvent(new Event("input", { bubbles: true }));
      select.dispatchEvent(new Event("change", { bubbles: true }));
      sync();
      close();
      button.focus();
    }
    button.onclick = () => (opened === control ? close() : open());
    button.onkeydown = (event) => {
      if (
        [
          "ArrowDown",
          "ArrowUp",
          "Home",
          "End",
          "Enter",
          " ",
          "Escape",
        ].includes(event.key)
      )
        event.preventDefault();
      if (event.key === "Escape" || event.key === "Tab") {
        close();
        return;
      }
      if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
        if (opened !== control) {
          open();
          return;
        }
        const values = enabled(),
          at = values.indexOf(active);
        active =
          event.key === "Home"
            ? values[0]
            : event.key === "End"
              ? values.at(-1)
              : values[
                  (at + (event.key === "ArrowDown" ? 1 : -1) + values.length) %
                    values.length
                ];
        render();
        return;
      }
      if (event.key === "Enter" || event.key === " ") {
        if (opened === control) commit(active);
        else open();
        return;
      }
      if (event.key.length === 1 && !event.ctrlKey && !event.metaKey) {
        search += event.key.toLowerCase();
        clearTimeout(searchTimer);
        searchTimer = setTimeout(() => (search = ""), 650);
        const index = enabled().find((i) =>
          select.options[i].label.toLowerCase().startsWith(search),
        );
        if (index != null) {
          if (opened !== control) open();
          active = index;
          render();
        }
      }
    };
    select.addEventListener("change", sync);
    select.addEventListener("focus", () => button.focus());
    select.form?.addEventListener("reset", () => queueMicrotask(sync));
    const observer = new MutationObserver(sync);
    observer.observe(select, {
      childList: true,
      subtree: true,
      attributes: true,
      characterData: true,
    });
    const onPopover = (event) => {
      if (event.detail !== button) close();
    };
    const control = { close, sync, select, wrap, menu, observer, onPopover };
    document.addEventListener("awarddesk:popover", onPopover);
    all.add(control);
    sync();
  }
  function scan() {
    for (const item of all) {
      if (!item.select.isConnected) {
        item.close();
        item.observer.disconnect();
        document.removeEventListener("awarddesk:popover", item.onPopover);
        item.menu.remove();
        all.delete(item);
      }
    }
    document
      .querySelectorAll("select:not([data-custom-select])")
      .forEach(enhance);
  }
  document.addEventListener("pointerdown", (e) => {
    if (
      opened &&
      !opened.wrap.contains(e.target) &&
      !opened.menu.contains(e.target)
    )
      opened.close();
  });
  window.addEventListener("resize", () => opened?.close());
  document.addEventListener(
    "scroll",
    (e) => {
      if (opened && !opened.menu.contains(e.target)) opened.close();
    },
    true,
  );
  document.addEventListener("close", () => opened?.close(), true);
  new MutationObserver(scan).observe(document.body, {
    childList: true,
    subtree: true,
  });
  scan();
})();
