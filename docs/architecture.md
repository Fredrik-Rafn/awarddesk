# Architecture

Start with the [guided walkthrough](code-walkthrough.md), then read
[design decisions](design-decisions.md) and [capacity/scaling](capacity.md).

Awarddesk is one Go process serving an embedded HTML/CSS/JavaScript frontend on
127.0.0.1. SQLite is the authoritative local store.

```text
Browser (web/assets)
    |
web handlers -- jobs queue --> fleet --> providers/eva
    |                            |           |
admin / registration -----------+       session + antibot
    |                            |           |
storage (SQLite)            proxy pool    EVA HTTP flow
    |                                        |
sessionstore (snapshots + OS locks)      outlook (OAuth/OTP)
```

## Package responsibilities

| Package | Responsibility |
| --- | --- |
| main.go | Composition, listener, startup and shutdown |
| internal/award | Query/result/progress/error contract; immutable result copies |
| internal/jobs | Queue, workers, deduplication, cancellation, timelines and history |
| internal/fleet | Account ownership, shared concurrency, background upkeep, retry policy |
| internal/providers/eva | EVA forms, authentication, booking, availability and mileage quotes |
| internal/registration | Durable bulk onboarding and checkpoint recovery |
| internal/outlook | OAuth refresh-token rotation, read-only IMAP and email validation |
| internal/exchange | Shared daily rate cache, exact decimal USD conversion and failure backoff |
| internal/proxies | Sticky proxy creation, exclusive leases, resume and cooldown |
| internal/session | TLS transport, cookie scopes, host restrictions, bounded HTTP |
| internal/sessionstore | Portable state adapter and cross-process account locks |
| internal/antibot | Security protocols actually required by EVA |
| internal/storage | SQLite schema, settings, migration, cache policy and analytics |
| internal/config | Database bootstrap and one-time environment import |
| internal/web | Local HTTP boundary, request tokens, admin authentication |
| internal/web/assets | Plain JavaScript, custom controls, search/admin rendering |
| cmd | Explicit live diagnostics |

The provider interface remains useful for testing: job scheduling can be tested
with deterministic fake providers without making airline or solver requests.

The EVA package separates account configuration (`provider.go`), login
(`login.go`), HTTP navigation (`navigation.go`), saved member state
(`member_state.go`), registration and flight pricing. Production flows use HTTP
and OAuth/IMAP; they do not launch browser automation.

Two search modes share authentication, account ownership, queueing and storage.
An empty query `mode` uses the original Miles & Cash implementation; `award`
routes to `award_search.go`. That flow separates availability, itinerary metadata,
selection, quotation and portal security into `award_*.go` files. Mode is part of
the query/cache identity, so the two products cannot reuse each other's results.
See [award flow](eva-awards.md) for the exact sequence and recovery boundaries.

Cross-package HTTP/SQLite/scheduling tests live in `tests/integration/`. Frontend
JavaScript tests live in `tests/browser/` and run with Node, without a browser.
Private Go unit tests and their fixtures stay beside their package. See the
[test guide](../tests/README.md) for the layout and offline commands.

Admin routing/authentication stays in `web/admin.go`; account edits, settings and
dashboard summaries live in `admin_accounts.go`, `admin_settings.go` and
`admin_overview.go`. Existing history/cache/registration handlers keep their own
files. These files share a package, so the refactor needs no extra exported APIs.

## Persistence and lifecycle

main opens SQLite, acquires the workspace lock, reads configuration, constructs
the fleet/jobs/onboarding services, restores history and binds the listener.
Maintenance starts after binding. Deferred cleanup cancels and joins workers
before closing providers and storage.

SQLite stores settings, accounts, scoped member snapshots, rotated Outlook tokens,
flight caches, jobs/timelines, admin session hashes, account runtime status and
registration checkpoints. OS locks supplement in-process ownership. Transactions
are short and never span an external HTTP or mailbox request.

Old environment/proxy/state files import once. Unsupported integration settings
and obsolete member snapshots are removed when storage opens; EVA accounts,
onboarding and permanent search history remain intact.

## API map

| Method/path | Purpose |
| --- | --- |
| GET /api/config | Capabilities, readiness and current request token |
| GET /api/airports?q=... | Local airport autocomplete |
| POST /api/searches | Submit a search; returns a job ID |
| GET /api/searches/{id} | Poll progress/previews/final results |
| DELETE /api/searches/{id} | Cancel the shared job |
| GET/POST/DELETE /api/logins/eva | Explicit fleet preparation status/start/cancel |
| /api/admin/session | Admin sign-in, status and sign-out |
| /api/admin/* | Accounts, prepare queues, registrations, settings, proxies, caches and analytics |

Mutation requests require X-CSRF-Token. Host/Origin checks restrict the local
boundary; admin operations also require a valid HttpOnly/SameSite cookie.
Public results/configuration exclude credentials.

Optional [demo mode](demo.md) accepts one explicitly configured HTTPS hostname
behind ngrok. `web/demo.go` requires a separate demo password for that hostname
and keeps admin/manual account-preparation routes localhost-only. Forwarding
headers never grant local access. The ordinary origin and request-token checks
also apply to public requests. The listener still binds to 127.0.0.1.

## Contention boundaries

The bounded FIFO separates capacity-wait and execution budgets. Account/proxy
release notifications wake waiters. A coalescing history writer keeps SQL outside
the scheduler lock and flushes admission before dispatch. Revision-aware polling
avoids resending unchanged results. See the walkthrough for exact limits and the
reliability guide for failure/backpressure rules.

Busy award searches can borrow up to two idle ready accounts for disjoint quote
assignments. Those helpers count toward the same global account-operation limit,
never wait for nested capacity, and have bounded deadlines that reserve time for
parent fallback. Each account keeps its own HTTP client, cookie jar, sticky proxy
and server session. Helpers return validated parsed quotes, not form state, and
cannot publish a subset into the full-query cache. The parent owns final result
ordering, optional tax enrichment, conversion, history and caching.
