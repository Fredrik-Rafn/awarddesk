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
