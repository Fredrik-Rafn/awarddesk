# ANA login

ANA is a login-only module in `internal/providers/ana`. It is retained in the
backend but hidden from the public page and settings panel. EVA is the active
public search provider. ANA does not implement availability or award pricing.

The server and default `go run ./cmd/ana-login` read persisted SQLite settings:
`ANA_MEMBER_NUMBER`, `ANA_PASSWORD`, `HYPER_API_KEY`, and optional proxy settings.
For a new workspace, these can be imported once using `.env.example`. Later
`.env` edits do not override the database. See [storage](storage.md).

When configured and background preparation is enabled, the server prepares a
session at startup and revalidates it every five minutes while successful.
The retained `/api/logins/ana` status/start/cancel API uses the saved-session
manager. The CLI instead performs a fresh bounded login and closes it afterward.
See [saved sessions](sessions.md).

For a temporary diagnostic using process variables, set the same credentials
and run `go run ./cmd/ana-login -env`. This bypasses SQLite without importing
or persisting the values. `AWARDDESK_PROXY` is optional;
`AWARDDESK_PROXY_FILE` selects a sticky proxy pool. Windows users can run
`./login-ana.ps1` to prompt for temporary credentials; it restores the previous
environment afterward. Go 1.25.5 or newer is required.

The command exits nonzero on failure, has a 150-second deadline per check and
supports Ctrl+C. It prints no passwords, tokens, member profiles or raw upstream
responses. Normal offline tests do not contact ANA.

## Implemented flow

The reference is the supplied `www.ana.co.jp-login.har` and the login JavaScript
captured within it. The capture is reference data, not runtime input.

1. Create a fresh, isolated cookie jar and load the European English homepage.
   When `HYPER_API_KEY` is configured, initialize SBSD (when present) and the web
   sensor using the shared Hyper adapter before authorization. ANA skips pixel
   generation; fresh login without that step was verified.
   ANA sets `Hyper.VerifyAtLogin=true`: in the successful browser capture the
   homepage sensor retains `_abck` marker `-1`; authorization first issues `0`.
   Three successful sensor submissions may therefore proceed to authorization.
   This does not count as login success. BA retains its existing marker check.
2. Fetch current frontend API client configuration from `/api-secret/client.json`.
3. POST `/auth/api/v2/authorization` with `usableState=3` and an empty
   `personalUpdateFlag`. Require status `1`, login state `3`, and a fresh token.
4. Submit the member number and web password once to
   `/auth/api/v2/sso-authentication`, with persistent login disabled.
5. Retain the authorization token if SSO returns an empty token, as in the
   capture. Accept a replacement token when supplied. Preserve server cookies
   and the response correlation ID in the same session.
6. GET `/amcmember-decrypt/api/v1/cookie?ctryCod=eu` and require the returned
   authenticated member number to match the requested account.

The flow ends here. It does not call reservation initialization, saved plans,
availability, pricing, or booking endpoints. The command closes its session on
success; the package returns an opaque `LoginSession` for future extension.

Two-step verification and browser handoffs return explicit errors. A four-digit
AMC PIN is rejected locally because it uses a different browser flow. Credential
POSTs are never automatically retried. Rate limits, cancellation, HTTPS host
restrictions and bounded response reads use the existing session layer.
Requests execute sequentially without a fixed delay between them. Transient GET
retry backoff and server-requested cooldowns still apply.

## CAPTCHA

The supplied successful login capture contains no CAPTCHA token in the SSO
payload. The login page does include Akamai browser telemetry. As requested,
ANA uses Hyper Solutions specifically for Akamai, through the same adapter as
BA. The adapter now derives request origins from the actual airline page and
rejects off-origin sensor/pixel URLs. It loads fresh scripts and cookies rather
than copying challenge material from the HAR.

ANA login does not call AnySolver. The existing AnySolver integration for BA
is unchanged. No supplied key or member
credential is embedded in source, fixtures, or launchers.

## Verification

Latest reliability checks: the configured proxy pool has not produced a
successful login. Four instrumented checks returned HTTP 403 at credentials;
a direct control returned `mfa_required`. Live attempts stopped at MFA.
See [reliability details](reliability.md) for timings, retries, pool semantics,
and the distinction between historical direct success and current results.

Performance update, 2026-10-20: a fresh live login completed in **3.95 seconds**
(prebuilt CLI runtime, excluding compilation), with no fixed request spacing,
no pixel solve, one session-scoped outbound-IP lookup, two SBSD generations and
three sensor generations. The authenticated member matched the requested account.
This is one measured run, not a guaranteed latency.

Hyper's [sensor guide](https://docs.hypersolutions.co/akamai-web/getting-started)
requires successive rounds to use updated cookies/context. Its
[SBSD guide](https://docs.hypersolutions.co/akamai-web/sbsd-introduction)
specifies two ordered payloads for passive SBSD. These are separate generations,
not a documented batch solve. The
[API reference](https://docs.hypersolutions.co/api-reference/akamai)
notes that pixel verification is often unnecessary.

The documented context-only second SBSD generation was also tested, but the
live endpoint rejected it with "missing script". The working script-based
format is retained without an extra fallback attempt on each login.

Offline tests cover exact request order and payloads, freshly loaded client
configuration, authorization/correlation propagation, empty and replacement SSO
tokens, authenticated member verification, MFA/browser handoffs, changed
responses, upstream errors, cancellation, invalid configuration, and no
credential retries. Fixtures are synthetic.

Before the performance update, a fresh live run using Hyper Solutions completed pixel, SBSD,
three web-sensor submissions, authorization, SSO login, and the authenticated
member check. The returned member number matched the requested temporary
account. No captured cookies or tokens were loaded, and no flight search was
performed. The command then closed the session.

`go test -timeout 45s ./...`, `go vet ./...`, the `cmd/ana-login` build, and
PowerShell launcher syntax validation passed. This verifies one direct live
login; proxy login, MFA, other accounts, and long-lived session reuse remain
unverified. A successful authorization response alone does not establish login.
