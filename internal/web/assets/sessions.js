"use strict";
// Separate login-only controls; this module never submits a flight search.
for (const airline of ["ana", "eva"]) (() => {
  const button = document.getElementById(`${airline}-login`);
  const cancel = document.getElementById(`${airline}-cancel`);
  const message = document.getElementById(`${airline}-status`);
  const setup = document.getElementById(`${airline}-setup`);
  let csrf, ready = false, running = false, timer;
  const controls = () => {
    button.disabled = !ready || running;
    cancel.hidden = !running;
  };
  async function request(method = "GET") {
    const response = await fetch(`/api/logins/${airline}`, {
      method, headers: { "X-CSRF-Token": csrf || "" },
    });
    const data = response.status === 204 ? null : await response.json();
    if (!response.ok) throw new Error(data.error || `${airline.toUpperCase()} login check unavailable`);
    return data;
  }
  async function refresh() {
    clearTimeout(timer);
    try {
      const status = await request();
      running = status.state === "running";
      message.textContent = status.message;
      if (status.finishedAt && status.state !== "running" && status.durationMs) {
        message.textContent += ` (${(status.durationMs / 1000).toFixed(1)}s; checked ${new Date(status.finishedAt).toLocaleTimeString()})`;
      }
      controls();
      if (running) timer = setTimeout(refresh, 1800);
    } catch (error) {
      message.textContent = error.message;
      controls();
      if (running) timer = setTimeout(refresh, 5000);
    }
  }
  button.addEventListener("click", async () => {
    running = true;
    controls();
    message.textContent = `Preparing ${airline.toUpperCase()} session…`;
    let failure;
    try { await request("POST"); }
    catch (error) { failure = error.message; }
    // Read server state even after a lost POST response; never repeat the POST.
    await refresh();
    if (failure && !running) message.textContent = failure;
  });
  cancel.addEventListener("click", async () => {
    cancel.disabled = true;
    try {
      await request("DELETE");
      message.textContent = `Cancelling ${airline.toUpperCase()} login check…`;
      await refresh();
    } catch (error) { message.textContent = error.message; }
    finally { cancel.disabled = false; }
  });
  (async () => {
    try {
      const response = await fetch("/api/config");
      if (!response.ok) throw new Error("Local server unavailable");
      const config = await response.json();
      csrf = config.csrf;
      ready = config[`${airline}LoginReady`];
      setup.hidden = ready;
      controls();
      if (ready) await refresh();
      else message.textContent = `${airline.toUpperCase()} credentials are not configured.`;
    } catch (error) { message.textContent = error.message; }
  })();
})();

