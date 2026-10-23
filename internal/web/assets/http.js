"use strict";



const workspaceHTTP = (() => {
  let token = "",
    refresh;
  const snapshots = new Map();
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
      const conditional = (!options.method || options.method === "GET") &&
        path.startsWith("/api/searches/");
      for (let attempt = 0; attempt < 2; attempt++) {
        const saved = conditional ? snapshots.get(path) : null;
        const response = await fetch(path, {
          ...options,
          credentials: "same-origin",
          cache: "no-store",
          headers: {
            "Content-Type": "application/json",
            ...options.headers,
            "X-CSRF-Token": token,
            ...(saved ? { "If-None-Match": saved.tag } : {}),
          },
        });
        if (response.status === 304 && saved) return saved.value;
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
          error.retryAfterSeconds = data?.retryAfterSeconds ||
            Number(response.headers?.get("Retry-After")) || 0;
          throw error;
        }
        if (path === "/api/config" && data?.csrf) token = data.csrf;
        if (conditional) {
          const tag = response.headers?.get("ETag");
          if (tag) {
            snapshots.delete(path);
            snapshots.set(path, { tag, value: data });
            if (snapshots.size > 4) snapshots.delete(snapshots.keys().next().value);
          }
        }
        return data;
      }
    },
  };
})();
