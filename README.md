# Awarddesk

Local airline search app: one Go process serves the backend, embedded frontend,
background session preparation and SQLite database. Works on Windows, macOS and
Linux. Requires Go 1.25.5 or newer; no Node, separate frontend server, C compiler
or external database is needed.

## Airline choices and scope

| Airline | Why it is included | Current support |
| --- | --- | --- |
| EVA Air | The supplied end-to-end captures include login, email verification, availability and mileage pricing, making it the main integration. | Public search UI, saved sessions, multiple accounts, one-way Miles & Cash estimates for 1–9 adults. |
| British Airways | The original integration provides an Avios award-search flow and a separate example of OAuth/PKCE authentication. | Experimental search provider retained in the backend when configured; hidden from the public selector. |
| ANA | Added to investigate Mileage Club authentication and reusable sessions independently of flight search. | Login and session verification only; no availability or award pricing. Hidden from the public page. |

EVA uses **`IS_MILES_MODE=false`**, as requested: Miles & Cash against a paid
fare, rather than an award ticket. The displayed estimate maximizes the cash
offset using miles, irrespective of the selected account's balance. Cash and
mileage totals apply to the entire search party.

## How the airline APIs were investigated

The starting evidence was browser DevTools HAR exports supplied for each airline,
plus exported EVA verification emails. Request methods, redirects, form fields,
cookies, embedded page JSON and response bodies were mapped into Go HTTP flows.
The implementation uses `tls-client`/`fhttp` for isolated HTTP sessions, HTML/JSON
parsers for fresh state, explicit diagnostic commands and sanitized stage timings
for live checks, and `httptest`/local fixtures for offline regression tests.
The application does not read HARs or depend on captured cookies at runtime.

**EVA:** the captures revealed separate member and booking sessions, Outlook OTP,
image CAPTCHA, Akamai checks, and Imperva/Reese84 booking security. Availability
contains cash fares, but mileage options arrive later through fare selection,
traveller/catalogue and checkout preparation (`ACTION=BOOK`). Those steps carry
rotating state and cannot be replaced with an availability-only request. Login
snapshots and complete searches are cached independently. Challenges included
late OTP delivery, OAuth refresh-token rotation, expired proxy identities, changing
security pages and partial pricing responses. Availability cash amounts are per
adult while checkout amounts are party totals; normalizing those units fixed
false `price_changed` failures. See [EVA flow](docs/eva.md),
[registration](docs/registration.md) and [solver comparisons](docs/solver-comparison.md).

**British Airways:** the captures exposed OAuth authorization with PKCE, hCaptcha,
an authorization-code exchange, and a separate classic booking session. Fresh
CSRF/form tokens and exact live selection values are carried into Avios quotes.
A Bearer header absent from the sanitized capture proved necessary in live
requests. Rotating form tokens, interstitial pages and high-demand responses
needed explicit parsing rather than treating every page as empty availability.
Hyper handles Akamai; AnySolver handles CAPTCHA. See [BA flow](docs/ba-flow.md).

**ANA:** the login HAR and captured frontend JavaScript exposed frontend client
configuration, authorization, SSO credential submission and an authenticated
member-cookie check. An empty token in a successful SSO response means retaining
the authorization token. Hyper's Akamai rounds depend on cookies from earlier
rounds; they are sequential, rather than one reusable batch. HTTP 403, OTP and
uncertain credential outcomes are separate failures, and are not counted as a
successful login. See [ANA flow](docs/ana-login.md) and [session lifecycle](docs/sessions.md).

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

BA/ANA credentials remain supported but are intentionally hidden from Settings.
For a new workspace, copy `.env.example` to `.env` before the first launch and
fill `BA_EMAIL`/`BA_PASSWORD` or `ANA_MEMBER_NUMBER`/`ANA_PASSWORD`, the solver keys,
and any proxy settings. These values import once. An existing migrated workspace
keeps its stored legacy settings; editing `.env` does not replace them.
The explicit ANA environment diagnostic below is available without changing SQLite.

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
go run ./cmd/ana-login -help
```

Diagnostic login commands use the same database. EVA checks lease an account
exclusively; explicit login checks can consume solver credits. The Outlook check
uses the first stored EVA account and never sends mail or marks messages read.
For an ANA check using temporary process variables rather than stored settings,
set `ANA_MEMBER_NUMBER`, `ANA_PASSWORD` and `HYPER_API_KEY`, optionally set
`AWARDDESK_PROXY` or `AWARDDESK_PROXY_FILE`, then run
`go run ./cmd/ana-login -env`. Windows users can use `./login-ana.ps1` to prompt
for these values. This mode neither imports nor updates workspace credentials.

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
- BA's live coverage is narrower than its advertised cabin/adult mappings:
  the historical verified case is one adult in economy, JFK–LAX. ANA has no
  flight-search implementation, and historical login successes do not guarantee
  current proxy, MFA or account acceptance.
- Session expiry is an estimate; server revocation or a lost sticky proxy can
  invalidate it earlier. A 30-minute cached fare may no longer be bookable.
- SQLite contains plaintext account, proxy and API secrets and portable tokens.
  Keep it private; use the clean shutdown procedure above before copying it.

See [architecture](docs/architecture.md), [storage](docs/storage.md),
[performance](docs/performance.md), [EVA reliability](docs/eva-reliability.md)
and [third-party dependencies](THIRD_PARTY.md) for details.
