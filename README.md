# Awarddesk

Local airline search app: one Go process serves the backend, embedded frontend,
background session preparation and SQLite database. Works on Windows, macOS and
Linux. Requires Go 1.25.5 or newer; no Node, separate frontend server, C compiler
or external database is needed.

## Learn the code

Read the [step-by-step code walkthrough](docs/code-walkthrough.md) first. It maps
search, login/OTP, proxy/session ownership, pricing, registration, caching and
automatic recovery to their implementing functions. Then read
[design decisions and alternatives](docs/design-decisions.md),
[capacity and scaling](docs/capacity.md) and the [architecture map](docs/architecture.md).

See [solver calls, proxy bandwidth and operating costs](docs/operating-costs.md)
for per-flow estimates, pricing assumptions and background renewal costs.

## Airline and scope

EVA Air is the focus because its Miles & Cash flow combines flight availability,
fare families and mileage offsets in a single booking journey. The integration
covers authentication, email verification, reusable sessions and pricing across
all eligible flights returned for a date.

EVA uses **`IS_MILES_MODE=false`**: Miles & Cash against a paid
fare, rather than an award ticket. The displayed estimate maximizes the cash
offset using miles, irrespective of the selected account's balance. Cash and
mileage totals apply to the entire search party.

## Reverse engineering the request flow

The implementation follows EVA's browser request flow from member login through
flight selection and mileage pricing. Browser DevTools was used to inspect
request order, redirects, form fields, cookies and response data, then identify
which values are fixed configuration and which must be generated for each session.

The flow has several distinct stages:

1. **Authentication:** initialize the member session, complete the security checks,
   submit credentials and verify the resulting authenticated state. Outlook OAuth
   and read-only IMAP retrieve the fresh email OTP when verification is required.
2. **Booking handoff:** carry the authenticated session into a fresh booking
   context while preserving its cookies, browser identity and sticky proxy.
3. **Availability:** submit route, date, cabin and passenger count, then parse
   flight recommendations and fare families from the returned page data.
4. **Mileage pricing:** follow fare selection, traveller/catalogue and checkout
   preparation (`ACTION=BOOK`) to retrieve the Miles & Cash options. Availability
   alone exposes cash fares, so these later state-dependent steps are required.
5. **Normalization:** map the responses into a shared result model, omit explicitly
   ineligible fares and express cash and mileage amounts for the whole party.

The main challenges are rotating form state, separate member and booking session
lifetimes, delayed OTP delivery and security interstitials that resemble ordinary
pages. Availability also reports cash per adult while checkout reports party
totals; these units are normalized before checking for a genuine price change.
Login snapshots and complete search results are cached separately.

Go's `tls-client`/`fhttp` provides isolated HTTP sessions, while HTML/JSON parsers
extract fresh state from each response. Hyper and AnySolver adapters handle the
security challenges encountered in the flow. Diagnostic commands and sanitized
stage timings support live investigation; local fixtures and `httptest` cover
parsing, state transitions and recovery without contacting the airline.

See [EVA flow](docs/eva.md), [registration](docs/registration.md) and
[solver comparisons](docs/solver-comparison.md).

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

### Configure credentials

For a fresh installation, open Admin and:

1. Set `HYPER_API_KEY` and `ANYSOLVER_API_KEY` in Settings. The Hyper subscription
   must allow the EVA domains and the challenge products used by the flow.
2. Add sticky HTTP proxies in Proxies, one per line, using
   `http://user:password@host:port` or `host:port:user:password`.
3. Add an EVA account with its password and Outlook email, OAuth client ID and
   refresh token. Read-only IMAP access must be enabled for that mailbox; its
   ordinary email password is not required. Bulk account setup is also available.
4. Prepare the account, or let background maintenance do so. Once ready, use the
   public search page. Change the initial admin password in Admin.

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
  EVA applies changes when each account next becomes idle.
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
one way. It follows the `IS_MILES_MODE=false` flow, including pricing
preparation, and shows maximum-offset estimates regardless of account balance.
It does not submit payment or ticketing. Actual award-ticket redemption is a
different flow.

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
prepares up to half the shared worker limit, capped at eight accounts at a time.
Idle ready accounts are reverified every five minutes. Transient preparation
failures, including credential/OTP failures, schedule a fresh check in 15–30 minutes;
longer provider pauses take precedence. After 15 consecutive failures an account
needs attention. Success resets the counter. Missing/expired sessions are prioritized;
retry counters and deadlines survive restarts. Manual Prepare can re-arm an account.
No OS cron or
Codex automation is required, and no flight searches run automatically.

Member sessions retire five minutes before the earlier of their EVAID cookie
expiry or configured maximum age (default 3h from creation). The cookie deadline
is an upper bound: server revocation may happen sooner. EVA's booking page declares
a separate 600-second booking timeout, which is not the member-login lifetime.
A fresh booking context is created for each uncached search. See
[storage and migration](docs/storage.md), [EVA details](docs/eva.md),
[performance measurements](docs/performance.md) and [reliability](docs/eva-reliability.md).

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
internal/registration/          Durable bulk onboarding and verification checkpoints
internal/providers/eva/          Login, booking security, availability, pricing
internal/outlook/                OAuth rotation and read-only IMAP OTP lookup
internal/proxies/                Sticky endpoint leasing and validation
internal/session/               HTTP transport, scoped cookies and egress pinning
internal/sessionstore/          Pluggable state storage and OS account locks
internal/antibot/               Hyper and AnySolver clients
internal/jobs/                  Bounded workers, progress, cancellation, history
internal/web/                   HTTP boundary, admin routes and focused handlers
internal/web/assets/            Embedded flight-search and admin pages
internal/config/                Database bootstrap and legacy import adapters
cmd/                            Explicit diagnostic commands
tests/integration/              HTTP/admin, SQLite and scheduler integration tests
tests/browser/                  Offline frontend JavaScript tests (Node)
```

Go unit tests stay beside the package whose private logic they check. Integration
and frontend tests are separate under `tests/`; see the [test guide](tests/README.md).
Within EVA, start at `provider.go` for configuration, `login.go` for authentication,
`navigation.go` for redirects/forms and `member_state.go` for saved member state.
Admin handlers follow the same rule: `admin.go` wires routes and authentication;
`admin_accounts.go`, `admin_settings.go` and `admin_overview.go` own those features.

## Verify and diagnose

```sh
go test -p 1 ./...
go vet -p 1 ./...
go run ./cmd/eva-login
go run ./cmd/outlook-check
go run ./cmd/eva-check -help
```

`go test` runs offline with temporary databases and simulated providers. It does
not consume solver credits. Node is optional for frontend checks; no browser
automation is used in the application's login, registration or search flows.

Diagnostic login commands use the same database. EVA checks lease an account
exclusively; explicit login checks can consume solver credits. The Outlook check
uses the first stored EVA account and never sends mail or marks messages read.

## Known limitations and edge cases

- This is a local single-server application, not an authenticated public service
  or a distributed scheduler. Concurrency is bounded and each account remains
  exclusive during its booking flow. More workers do not guarantee more capacity.
- EVA supports one-way searches for adults only, without round trips, children,
  infants or first class. Explicitly ineligible Miles & Cash fares are omitted.
  No payment or ticketing request is sent, but pricing uses checkout preparation.
- Catalogue and pricing depend on EVA's response times and the number of distinct
  fare groups. Saved login sessions avoid authentication, not all booking requests.
- Airline markup, security challenges and residential proxy exits can change.
  OTP delivery may be delayed; uncertain credential/OTP/checkout outcomes are
  not blindly replayed. Genuine price changes can still invalidate an offer.
- The 22 October 2026 baseline completed 423/500 searches (84.6%, including 21
  valid empty results). The per-adult/party fix subsequently passed 7/7 targeted
  live checks; the full 500-search test has not been repeated after that fix.
  TPE–ICN handoffs and some booking-data responses remain unresolved failures.
- Session expiry is an estimate; server revocation or a lost sticky proxy can
  invalidate it earlier. A 30-minute cached fare may no longer be bookable.
- SQLite contains plaintext account, proxy and API secrets and portable tokens.
  Keep it private; use the clean shutdown procedure above before copying it.

See [architecture](docs/architecture.md), [storage](docs/storage.md),
[performance](docs/performance.md), [EVA reliability](docs/eva-reliability.md)
and [third-party dependencies](THIRD_PARTY.md) for details.
