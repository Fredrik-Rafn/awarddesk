"use strict";
let registrationPage = 1,
  registrationLoading = false,
  registrationBatch = [],
  registrationGeneration = 0;
function resetRegistrationBatch() {
  registrationGeneration++;
  registrationBatch = [];
  registrationPage = 1;
  for (const id of [
    "registration-total",
    "registration-list",
    "registration-pages",
    "registration-message",
  ])
    $(id).replaceChildren();
}
function trackRegistration(id) {
  if (!registrationBatch.includes(id)) registrationBatch.push(id);
  registrationGeneration++;
  refreshRegistrations();
}
async function retryRegistration(item) {
  if (
    item.canRestart &&
    !(await confirmAction({
      title: "Restart expired signup?",
      message:
        "The previous verification window has expired. Only continue after checking that the mailbox has no usable EVA verification email and this membership was not completed elsewhere. This archives the old checkpoint and submits a fresh signup.",
      confirmLabel: "Restart signup",
    }))
  )
    return;
  await api(
    "/api/admin/registrations/" + (item.canRestart ? "restart" : "retry"),
    {
      method: "POST",
      body: JSON.stringify(
        item.canRestart
          ? { id: item.id, confirmRestart: true }
          : { id: item.id },
      ),
    },
  );
  trackRegistration(item.id);
  message("Account setup resumed", "success");
  await refresh();
}
$("retry-all-setups").onclick = async () => {
  const button = $("retry-all-setups");
  button.disabled = true;
  button.setAttribute("aria-busy", "true");
  button.classList.add("is-loading");
  try {
    const result = await api("/api/admin/registrations/retry-all", {
      method: "POST",
    });
    registrationBatch = [...new Set([...registrationBatch, ...result.ids])];
    registrationGeneration++;
    message(
      result.queued +
        " account setups queued" +
        (result.skipped
          ? " · " +
            result.skipped +
            " require individual inspection before continuing"
          : ""),
      "success",
    );
    await refreshRegistrations();
    await refresh();
  } catch (error) {
    message(error.message);
  } finally {
    button.removeAttribute("aria-busy");
    button.classList.remove("is-loading");
    button.disabled = false;
  }
};
async function refreshRegistrations() {
  if (
    !polling ||
    registrationLoading ||
    !registrationBatch.length ||
    currentView() !== "accounts"
  )
    return;
  registrationLoading = true;
  const generation = registrationGeneration;
  try {
    const batches = [];
    for (let i = 0; i < registrationBatch.length; i += 500)
      batches.push(registrationBatch.slice(i, i + 500));
    const responses = await Promise.all(
      batches.map((ids) =>
        api("/api/admin/registrations?ids=" + ids.join(",")),
      ),
    );
    const data = { items: responses.flatMap((response) => response.items) };
    if (
      !polling ||
      generation !== registrationGeneration ||
      currentView() !== "accounts"
    )
      return;
    const active = data.items.filter((item) =>
      ["queued", "running"].includes(item.status),
    );
    const list = $("registration-list");
    list.replaceChildren();
    $("registration-total").textContent = active.length
      ? active.length + " in progress"
      : "";
    $("registration-pages").replaceChildren();
    if (!active.length) {
      registrationBatch = [];
      $("registration-message").textContent =
        "Batch finished. Accounts needing attention are shown in the accounts list.";
      await refresh();
      return;
    }
    const pages = Math.ceil(active.length / 25);
    registrationPage = Math.min(registrationPage, pages);
    if (pages > 1)
      renderPagination(
        $("registration-pages"),
        registrationPage,
        pages,
        (page) => {
          registrationPage = page;
          refreshRegistrations();
        },
      );
    const heading = el("h4", "Current account creation session");
    const rows = active
      .slice((registrationPage - 1) * 25, registrationPage * 25)
      .map((item) => {
        const state = el("div");
        state.append(
          el(
            "span",
            item.status === "queued" ? "Queued" : "Creating",
            "status-pill status-" + item.status,
          ),
        );
        const progress = el(
          "div",
          undefined,
          "job-progress registration-progress",
        );
        progress.setAttribute("role", "progressbar");
        progress.setAttribute("aria-label", "Account setup in progress");
        progress.append(el("span", undefined, "indeterminate"));
        state.append(progress);
        return [
          item.email,
          state,
          cell(item.stage, item.message),
          date(item.updatedAt),
        ];
      });
    list.append(
      heading,
      table(["Mailbox", "Status", "Current step", "Updated"], rows),
    );
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
  const generation = registrationGeneration;
  const button = $("start-registration");
  button.disabled = true;
  try {
    const result = await api("/api/admin/registrations", {
      method: "POST",
      body: JSON.stringify({
        credentials,
        acceptTerms: true,
      }),
    });
    $("registration-credentials").value = "";
    if (generation !== registrationGeneration || currentView() !== "accounts")
      return;
    $("registration-message").textContent =
      result.added + " queued · " + result.skipped + " already present";
    registrationBatch = [
      ...new Set([...registrationBatch, ...(result.ids || [])]),
    ];
    registrationGeneration++;
    registrationPage = 1;
    await refreshRegistrations();
  } catch (e) {
    message(e.message);
  } finally {
    button.disabled = false;
  }
};
setInterval(() => {
  if (polling && currentView() === "accounts") refreshRegistrations();
}, 3000);
window.addEventListener("hashchange", () => {
  resetRegistrationBatch();
});
