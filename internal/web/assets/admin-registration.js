"use strict";
let registrationPage = 1,
  registrationLoading = false;
async function refreshRegistrations() {
  if (!polling || registrationLoading) return;
  registrationLoading = true;
  try {
    const data = await api("/api/admin/registrations?page=" + registrationPage);
    if (!polling) return;
    $("registration-total").textContent = data.total + " total";
    const pages = Math.max(1, Math.ceil(data.total / data.limit));
    renderPagination(
      $("registration-pages"),
      data.page,
      pages,
      async (page) => {
        registrationPage = page;
        await refreshRegistrations();
      },
    );
    const list = $("registration-list");
    const focusedRow = list.contains(document.activeElement)
      ? document.activeElement.closest("[data-registration-id]")?.dataset
          .registrationId
      : null;
    if (
      typeof changedSnapshot === "function" &&
      !changedSnapshot("registrations", data.items)
    )
      return;
    list.replaceChildren();
    if (!data.items.length) {
      list.append(
        el(
          "p",
          "No account creations yet. Your onboarding history will stay here.",
          "empty-note",
        ),
      );
      return;
    }
    for (const item of data.items) {
      const card = el("article", undefined, "registration-row"),
        copy = el("div"),
        top = el("div", undefined, "registration-row-title");
      card.dataset.registrationId = item.id;
      top.append(
        el("strong", item.email),
        el("span", item.status, "status-pill status-" + item.status),
      );
      copy.append(
        top,
        el("p", item.stage + " · " + item.message),
        el(
          "small",
          "Added " +
            date(item.createdAt) +
            " · Updated " +
            date(item.updatedAt),
        ),
      );
      if (item.error && ["attention", "failed"].includes(item.status))
        copy.append(
          el(
            "p",
            typeof item.error === "string"
              ? item.error
              : item.error.message || JSON.stringify(item.error),
            "registration-error",
          ),
        );
      if (["running", "queued"].includes(item.status)) {
        const progress = el(
          "div",
          undefined,
          "job-progress registration-progress",
        );
        progress.setAttribute("role", "progressbar");
        progress.setAttribute(
          "aria-label",
          item.status === "queued"
            ? "Account creation queued"
            : "Account creation in progress",
        );
        progress.append(el("span", undefined, "indeterminate"));
        copy.append(progress);
      }
      card.append(copy);
      if (item.status === "failed" || item.status === "attention") {
        const retry = el(
          "button",
          item.status === "attention" ? "Resume verification" : "Retry setup",
          "secondary",
        );
        retry.type = "button";
        retry.onclick = async () => {
          retry.disabled = true;
          try {
            await api("/api/admin/registrations/retry", {
              method: "POST",
              body: JSON.stringify({ id: item.id }),
            });
            await refreshRegistrations();
          } catch (e) {
            message(e.message);
          } finally {
            retry.disabled = false;
          }
        };
        card.append(retry);
      }
      list.append(card);
    }
    if (focusedRow) {
      const replacement = [...list.children]
        .find((row) => row.dataset.registrationId === focusedRow)
        ?.querySelector("button");
      if (replacement) replacement.focus();
      else {
        $("registration-total").tabIndex = -1;
        $("registration-total").focus({ preventScroll: true });
      }
    }
  } catch (e) {
    $("registration-message").textContent = e.message;
  } finally {
    registrationLoading = false;
  }
}
$("registration-form").onsubmit = async (e) => {
  e.preventDefault();
  const credentials = $("registration-credentials").value;
  if (
    !(await confirmAction({
      title: "Create EVA accounts?",
      message:
        "This submits real EVA memberships for the listed mailboxes using generated profiles. Each account is verified through Outlook, saved locally and prepared for searches. Existing imports are skipped.",
      confirmLabel: "Create accounts",
    }))
  )
    return;
  const button = $("start-registration");
  button.disabled = true;
  try {
    const result = await api("/api/admin/registrations", {
      method: "POST",
      body: JSON.stringify({
        credentials,
        acceptTerms: $("registration-consent").checked,
      }),
    });
    $("registration-credentials").value = "";
    $("registration-consent").checked = false;
    $("registration-message").textContent =
      result.added + " queued · " + result.skipped + " already present";
    registrationPage = 1;
    await refreshRegistrations();
  } catch (e) {
    $("registration-message").textContent = e.message;
  } finally {
    button.disabled = false;
  }
};
setInterval(() => {
  if (polling && currentView() === "accounts") refreshRegistrations();
}, 3000);
window.addEventListener("hashchange", () => {
  if (polling && currentView() === "accounts") refreshRegistrations();
});
