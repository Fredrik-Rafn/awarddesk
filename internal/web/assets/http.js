"use strict";
// A token rejection happens in the server middleware before any action runs.
// Only that explicit response may refresh the token and replay once. Network
// failures, provider errors and all other responses are never replayed here.
const workspaceHTTP = (() => {
  let token = "",
    refresh;
  async function refreshToken() {
    if (!refresh)
      refresh = (async () => {
        const response = await fetch("/api/config", {
          cache: "no-store",
          credentials: "same-origin",
        });
        const value = await response.json();
        if (!response.ok || !/^[a-f0-9]{64}$/.test(value.csrf || ""))
          throw Error("Could not refresh the local request token");
        token = value.csrf;
      })().finally(() => (refresh = null));
    return refresh;
  }
  return {
    async request(path, options = {}) {
      for (let attempt = 0; attempt < 2; attempt++) {
        const response = await fetch(path, {
          ...options,
          credentials: "same-origin",
          cache: "no-store",
          headers: {
            "Content-Type": "application/json",
            ...options.headers,
            "X-CSRF-Token": token,
          },
        });
        const data = response.status === 204 ? null : await response.json();
        if (
          response.status === 403 &&
          data?.code === "request_token_invalid" &&
          attempt === 0
        ) {
          await refreshToken();
          continue;
        }
        if (!response.ok) {
          const error = Error(data?.error || data?.message || "Request failed");
          error.status = response.status;
          error.code = data?.code;
          throw error;
        }
        if (path === "/api/config" && data?.csrf) token = data.csrf;
        return data;
      }
    },
  };
})();
