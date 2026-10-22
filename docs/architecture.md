# Architecture

Awarddesk is one Go process serving an embedded HTML/CSS/JavaScript frontend on
`127.0.0.1`. SQLite is the authoritative local store. No frontend build server,
external database or OS cron is required.

```text
main.go                         composition and shutdown
  |
internal/web                    local HTTP API, admin authentication, embedded UI
  |----------------------|-----------------------|
internal/jobs            internal/fleet          internal/registration
queue, results, history  EVA accounts + sessions  durable onboarding checkpoints
  |                      |
award.Provider           providers/eva
  |                      |-----------------------|
providers/ba             internal/outlook         internal/proxies
legacy account           OAuth + read-only OTP    sticky leases + egress checks

internal/login --> providers/ana.Manager --> providers/ana
status + upkeep    exclusive saved session       login-only

Shared: internal/award, internal/session, internal/sessionstore,
        internal/antibot, internal/storage, internal/config
```

## Boundaries

- `award` defines queries, capabilities, normalized results, progress and errors.
  Provider packages own airline URLs, fresh form state and parsers; they do not
  depend on web handlers. ANA intentionally does not implement flight search.
- `session` owns HTTP transport, cookie scoping, allowed hosts, response bounds
  and safe transport retries. `antibot` owns solver protocols. Credentials and
  session bodies never become public progress metadata.
- `fleet` schedules EVA accounts, session preparation and bounded account failover.
  Each account has one live booking-flow owner. Different accounts run concurrently.
  BA serializes its own single configured account inside its provider.
- `registration` persists onboarding checkpoints, so a lost browser tab does not
  lose an account awaiting verification. Retrying a checkpoint is different from
  resubmitting registration; uncertain submissions require inspection.
- `jobs` owns search queueing, deduplication, progress snapshots, cancellation and
  permanent history. It uses the provider interface, not airline-specific HTTP.
- `storage` owns SQLite settings, accounts, portable JSON state and history.
  `sessionstore` supplies storage adapters and OS locks. No SQL transaction spans
  an airline, mailbox or solver request.
- `config` handles first-run import, database location and legacy provider wiring.
  The live EVA fleet builds per-account providers from SQLite and refreshes
  configuration when accounts become idle. BA/ANA configuration needs a restart.
- `web` owns local request validation, CSRF, admin cookies and API serialization.
  Static pages are embedded at compile time, so source edits need a restart.

## Lifecycle and persistence

`main.go` opens/migrates SQLite, acquires the workspace server lock, constructs
providers and services, restores search history, then binds the localhost listener.
Only after binding does it start EVA maintenance and registration workers.
ANA maintenance remains supported when configured and enabled.

On shutdown, the HTTP server stops, background services cancel and join their
workers, and storage closes. Interrupted search jobs are recorded rather than
automatically replayed at startup.

The database stores:
- Global settings and proxies, individual EVA/Outlook account credentials.
- Portable member sessions with scoped cookies, browser identity and exact proxy.
- Rotated OAuth refresh tokens and registration checkpoints.
- Temporary complete-result caches, permanent search results/timelines and admin
  cookie hashes.

Flight caches default to 30 minutes; history has no retention deletion. A saved
member login and a booking context have different lifetimes. Each uncached EVA
search creates a fresh booking context. See [storage](storage.md),
[EVA](eva.md) and [ANA session lifecycle](sessions.md).

## HTTP surface

| Method and path | Purpose |
| --- | --- |
| `GET /api/config` | Capabilities, readiness flags and request token |
| `GET /api/airports?q=...` | Airport autocomplete |
| `POST /api/searches` | Queue an EVA or configured legacy BA search |
| `GET /api/searches/{id}` | Progress and normalized result |
| `DELETE /api/searches/{id}` | Cancel a search |
| `GET /api/logins/{eva\|ana}` | Current verification status |
| `POST /api/logins/{eva\|ana}` | Start/coalesce a login check |
| `DELETE /api/logins/{eva\|ana}` | Cancel that check |
| `/api/admin/session` | Admin sign-in, sign-out and session status |
| `/api/admin/*` | Authenticated accounts, preparation, onboarding, settings, caches and analytics |

Mutation requests require the current `X-CSRF-Token`; the server also validates
Host and Origin. Admin endpoints additionally require the admin cookie. Public
configuration/results exclude credentials. The public provider selector shows
EVA only; hidden BA/ANA functionality remains implemented.

## Adding or maintaining a provider

Keep airline-specific code under `internal/providers/<airline>`. Implement
`award.Provider` only when real search and pricing responses are supported.
Register it with the jobs service in the composition root; use a fleet adapter
only when multiple account scheduling is needed. Keep login-only integrations
separate until they support search.

Add parser and lifecycle fixtures beside the implementation. Normal tests are
offline; explicit commands in `cmd/` perform live diagnostics and can consume
solver credits. Keep real captures, tokens, keys and generated data out of Git.
