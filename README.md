# Awarddesk

Local airline search app: one Go process serves the backend, embedded frontend,
background session preparation and SQLite database. Works on Windows, macOS and
Linux. Requires Go 1.25.5 or newer; no Node, separate frontend server, C compiler
or external database is needed.

## Run

```sh
git clone https://github.com/Fredrik-Rafn/awarddesk.git
cd awarddesk
go run .
```

Open http://127.0.0.1:8080 for flight searches, and
http://127.0.0.1:8080/admin for administration. The initial admin password is
**admin1234**, with no username. It can be changed in Admin.
Use `go run . -port 8090` for another local port. Ctrl+C stops the server.

On the first run, existing `.env`, `proxies.txt` and valid `.state/**/*.json`
files are imported automatically. After that **SQLite is authoritative**;
editing old files does not override Admin changes. An empty checkout can be
configured entirely in Admin. The old files are retained as migration backups.
Do not run the previous file-based version against an already migrated workspace.

To move your configured workspace to another computer, stop Awarddesk cleanly
and securely copy `data/awarddesk.db` into the new checkout's `data` directory.
Do not copy only the main database file while its WAL is active. Git contains no
accounts, keys, proxies or saved sessions. Live sessions still require their
original proxy/IP to remain valid. No machine-specific encryption is used.

## Admin

- Edit, enable or remove existing EVA accounts, including their individual Outlook
  OAuth client ID and refresh token. Saved secrets load masked, with eye buttons
  to reveal them. Empty a settings value and save to remove it.
- Bulk-create EVA accounts from Outlook credential lines. Generated profiles,
  email verification, email-login activation and saved-session preparation run
  automatically with separate sticky proxies and the shared worker limit.
- Inspect saved member sessions, expiry estimates, preparation status and errors;
  prepare or invalidate a session explicitly. Busy accounts cannot be edited.
- Replace the sticky Evomi proxy list. Each fresh login selects an available
  endpoint with a new random locked ID; each saved session keeps its exact proxy.
- Change Hyper/AnySolver keys, solver selection, caching and concurrency settings.
  EVA applies changes when each account next becomes idle. Legacy ANA/BA values
  remain stored but are hidden from Settings.
- Explore the activity chart, success/live reliability rates, median/P95 timings,
  cache use, recovered successes, route rankings and failure stages. Select a time
  window from one hour to all retained history.
- Filter, sort and paginate all retained searches; inspect results and recovery
  events, or export the matching history as CSV.
- Inspect cached flight results and ongoing search jobs, cancel jobs and
  remove cached results. Search history, full results and statistics are permanent.
  Disable flight caching with its pill switch to confirm and clear temporary
  cached results immediately; history and saved logins remain intact.

Admin uses a hashed password, HttpOnly/SameSite cookies, CSRF/origin checks and
login throttling. API keys and account passwords/tokens are masked in forms and
excluded from public configuration and job data. The app binds to localhost.
Admin sign-in lasts 12 hours. Request tokens have no timed expiry; after a server
restart the browser refreshes its token and retries once only when middleware
explicitly rejected the request before executing it. Lost responses are not replayed.

See [bulk account creation](docs/registration.md) for import format and recovery.

## Search and session behavior

**EVA Air Miles & Cash** supports economy, premium economy and business, 1–9 adults,
one way. It follows the supplied `IS_MILES_MODE=false` flow, including pricing
preparation, and shows maximum-offset estimates regardless of account balance.
It does not submit payment or ticketing. The public page offers EVA only. Legacy
ANA and BA modules remain available in the codebase. Actual award-ticket
redemption is a different flow.

Complete identical EVA searches are cached for **30 minutes from availability
retrieval**. Errors and partial results are never cached. Admin can disable and
clear this cache. The API also accepts `refresh: true` for a live request.
Cache entries are account-specific; identical ongoing queries share one
job. Cancelling a shared job cancels it for every caller using that job ID.

Up to **four accounts** work concurrently by default, configurable from 1 to 512.
Each account/session remains exclusive throughout a booking flow. Additional
searches queue; one account cannot provide multiple independent live flows at once.
Ready sessions are preferred, then rotated by last use. Safe preparation failures
may use another account, at most three accounts per job. Pricing, credential and
OTP requests with uncertain outcomes are not blindly replayed.

While Awarddesk is running, an in-process scheduler checks every 30 seconds and
prepares up to two accounts at a time, within the configured concurrency limit.
Idle ready accounts are reverified every five minutes. Transient preparation
failures back off; credential/OTP or uncertain login outcomes need attention.
The manual Prepare action resumes an account after inspection. No OS cron or
Codex automation is required, and no flight searches run automatically.

Member sessions retire five minutes before the earlier of their EVAID cookie
expiry or configured maximum age (default 3h from creation). The cookie deadline
is an upper bound: server revocation may happen sooner. EVA's booking page declares
a separate 600-second booking timeout, which is not the member-login lifetime.
A fresh booking context is created for each uncached search. See
[storage and migration](docs/storage.md), [EVA details](docs/eva.md),
[performance measurements](docs/performance.md) and [reliability](docs/reliability.md).

## Settings

| Admin setting | Default / purpose |
| --- | --- |
| `EVA_RESULT_CACHE_TTL` | `30m`; `0s` disables caching; maximum `24h` |
| `FLIGHT_CACHE_ENABLED` | `true`; disabling in Admin confirms and immediately clears cached flights |
| `SEARCH_CONCURRENCY` | `4`; 1–512 independent accounts |
| `EVA_SESSION_MAX_AGE` | `3h`; a conservative maximum, not a guaranteed server lifetime |
| `EVA_BACKGROUND_ENABLED` | `true`; prepare and periodically verify sessions |
| `EVA_IMPERVA_SOLVER` | `auto`: Hyper first, AnySolver fallback for generation errors |
| `HYPER_API_KEY`, `ANYSOLVER_API_KEY` | Solver credentials |

The database defaults to `data/awarddesk.db`. `AWARDDESK_DB` is an optional
bootstrap environment variable selecting a different database path. It is not a
runtime setting. Keep the database and its lock/WAL files on a local filesystem.
Only one Awarddesk server should use a workspace at a time; local OS locks prevent
simultaneous session use and release immediately when a process exits.

## Code overview

```text
main.go                         Startup, lifecycle and provider wiring
internal/storage/               SQLite schema, migration, state and job persistence
internal/fleet/                 EVA account scheduling, failover and maintenance
internal/providers/eva/          Login, booking security, availability, pricing
internal/providers/ana/          ANA login and saved-session manager
internal/providers/ba/           Experimental BA integration
internal/outlook/                OAuth rotation and read-only IMAP OTP lookup
internal/proxies/                Sticky endpoint leasing and validation
internal/session/               HTTP transport, scoped cookies and egress pinning
internal/sessionstore/          Pluggable state storage and OS account locks
internal/antibot/               Hyper and AnySolver clients
internal/jobs/                  Bounded workers, progress, cancellation, history
internal/web/assets/            Embedded flight-search and admin pages
internal/config/                Database bootstrap and legacy import adapters
cmd/                            Explicit diagnostic commands
```

## Verify and diagnose

```sh
go test -p 1 ./...
go vet -p 1 ./...
go run ./cmd/eva-login
go run ./cmd/outlook-check
go run ./cmd/eva-check -help
```

Diagnostic login commands use the same database. EVA checks lease an account
exclusively; explicit login checks can consume solver credits. The Outlook check
uses the first stored EVA account and never sends mail or marks messages read.
No live multi-account success rate is claimed before more real accounts are added.
