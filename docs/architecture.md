# Architecture

Awarddesk is a single Go application with an embedded static frontend.
There is no external persistence layer. The server binds to `127.0.0.1`.

```text
Browser: internal/web/assets
          |
Local API: internal/web
          |---------------------------|
internal/login                    internal/jobs
ANA check status                  BA search queue/results
          |                           |
providers/ana                     providers/ba
          |---------------------------|
internal/session + internal/antibot
          |
Airline APIs / Hyper Solutions / AnySolver (BA only)
```

`main.go` is the composition root: it loads the local `.env` through `internal/config`
(existing process variables take precedence), reads settings, constructs
providers and lifecycle services, and starts the server. Provider packages own
airline-specific URLs, payloads, authentication state, and parsers. HTTP sessions
are isolated per operation. Shared transport and solver adapters contain no
member credentials.

## Login-only ANA module

The frontend uses a dedicated login panel, not the flight-search selector.
`internal/login` owns the background task, progress, cancellation, deadline,
and cooldown. The checker calls `ana.Provider.Login`, verifies the member, and
stores the member-verified session through `ana.Manager` and `sessionstore`.
The HTTP client closes after use; cookies, token, browser identity and the exact
proxy session survive in an encrypted file. Only public status reaches the UI.

Configuration is independent: ANA can run without BA credentials and vice versa.
No credentials are accepted from the browser or returned in API responses.
Reloading the page resumes polling an active login check.
Concurrent starts share that check; recent successes retain their original
timestamp. `internal/proxies` supplies an exclusive locked Evomi lease per
attempt when the editable pool file is configured. See
[reliability and capacity limits](reliability.md).

## API

| Method and path | Purpose |
| --- | --- |
| `GET /api/config` | Provider capabilities, configuration flags, CSRF token |
| `GET /api/logins/ana` | Current ANA login-check status |
| `POST /api/logins/ana` | Start a check; returns 202 immediately |
| `DELETE /api/logins/ana` | Request cancellation |
| `GET /api/airports?q=...` | Airport autocomplete |
| `POST /api/searches` | Submit an existing BA search |
| `GET /api/searches/{id}` | Search progress/result |
| `DELETE /api/searches/{id}` | Cancel search |

POST and DELETE require `X-CSRF-Token` obtained from `/api/config`. Search POSTs
require JSON. Host and Origin checks restrict access to the local UI.

ANA statuses: `idle → running → verified / failed / cancelled`.
Only one check can run at a time. The latest status lives until server restart
or another check. “Verified” records the last successful validation/save; the
session is checked again before use, rather than trusting that UI status.

The in-process maintenance loop starts after the localhost listener is bound,
then checks every five minutes. OTP and failures pause the loop for that process
until manually retried. Future ANA searches must call `Manager.WithSession`.
See [session persistence](sessions.md) for locking, expiry and storage behavior.

## Extending providers

Keep provider-specific behavior under `internal/providers/<airline>`. ANA login
does not implement the flight-search interface yet. A future ANA search module
must implement `award.Provider`, normalize real responses, add parser fixtures,
and then register with `jobs.New`. Do not expose it as a search provider before
that exists.

Tests next to the modules cover behavior with synthetic/local fixtures. Normal
`go test ./...` never contacts airlines or consumes solver credits. The separate
ANA CLI is the explicit live-check entry point.
