# Code walkthrough: learning Awarddesk

This is a reading guide for the current implementation. Follow the functions in
the listed order, then read their adjacent tests. Rules below describe implemented
behavior; [capacity and scaling](capacity.md) labels future work separately.

## Suggested reading order

1. [main.go](../main.go): identify the services created at startup.
2. [award/model.go](../internal/award/model.go): learn Query, Result, Fare, Progress,
   Recovery and Error before following requests.
3. [web/server.go](../internal/web/server.go): find submit and Handler.
4. [jobs/service.go](../internal/jobs/service.go): follow Submit, work, execute.
5. [fleet/fleet.go](../internal/fleet/fleet.go): follow Search, claim and outcome.
6. [eva/login.go](../internal/providers/eva/login.go): follow withSession,
   sessionAttempts and ensureLocked.
7. [eva/search.go](../internal/providers/eva/search.go): follow Search into
   searchAwardWithTaxes, then [award_search.go](../internal/providers/eva/award_search.go).
8. [award_parallel.go](../internal/providers/eva/award_parallel.go),
   [award_taxes.go](../internal/providers/eva/award_taxes.go) and
   [exchange.go](../internal/exchange/exchange.go): follow parallel mileage,
   optional tax estimates and USD conversion.
9. Return to storage, maintenance, registration and the frontend with that path
   in mind. Use [design decisions](design-decisions.md) to understand the tradeoffs.

## Startup and shutdown

In main.run:

1. Parse/validate the port before opening storage. Help exits without migration.
2. Open the database selected by AWARDDESK_DB, default data/awarddesk.db.
   config.Database performs one-time environment/proxy/state import.
3. Acquire the workspace server lock; a second server cannot own the same workspace.
4. Build the EVA fleet, search job service and registration service. Jobs restore
   persisted history, marking previously running/queued jobs interrupted.
5. Build the local web server and explicit EVA login-check facade.
6. Bind 127.0.0.1, then start fleet upkeep and registration workers.
7. On cancellation, stop HTTP, cancel/join services, close retained provider
   connections and close SQLite. Pending credential requests are not replayed.

Read [config/database.go](../internal/config/database.go),
[storage/store.go](../internal/storage/store.go),
[storage/migrate.go](../internal/storage/migrate.go),
[registration/service.go](../internal/registration/service.go) and the deferred
cleanup calls in main. Startup cleanup removes obsolete settings/member state,
not EVA accounts, registration history or search history.

## Flight search, end to end

The main page starts in **Award tickets** mode and sends `mode: "award"`.
**Miles & Cash** remains a secondary option. For backwards compatibility, an API
query with an empty mode still selects Miles & Cash; API callers wanting award
prices must send the award mode explicitly.

### 1. Browser and HTTP boundary

[app.js](../internal/web/assets/app.js) builds provider, mode, route, date, cabin,
adult count and refresh fields. [http.js](../internal/web/assets/http.js) sends
JSON with the current request token. [server.go](../internal/web/server.go) checks
Host, Origin, token, body size, JSON shape and query before submitting a job.

Award queries support 1–6 adults and dates from today through 360 days ahead.
Miles & Cash supports 1–9 adults through one year ahead. Both support one-way
routes in economy, premium economy and business; infants, children, round trips
and first class are unsupported. Airport suggestions do not prove route service.

A stale token receives one browser refresh/retry only after an explicit middleware
rejection before the action. Network errors do not authorize mutation replay.

### 2. Job, cache and capacity

[jobs/service.go](../internal/jobs/service.go), Submit, coalesces identical active
queries and checks complete result caches before admitting live work. The browser
receives a job ID immediately and polls instead of keeping a pricing HTTP request
open. The API can set `refresh: true` to bypass the result cache.

[queue.go](../internal/jobs/queue.go) admits at most 1,024 queued jobs, with a
30-minute wait budget. The 15-minute execution budget begins when the fleet first
claims an account; failovers share the remaining time. The logical execution
deadline also lets quotation helpers reserve time for primary-account fallback.
The concurrency setting applies live and is shared with account preparation and
registration. Lowering it lets active work drain.

[result_cache.go](../internal/providers/eva/result_cache.go) keys results by account
configuration and the full query, including mode and passenger count. Refresh is
normalized away in cache identity. Only complete successful results, including
recognized empty availability, are cached. Default TTL is 30 minutes from the
availability check, not final pricing. Disabling caching clears temporary entries
and blocks late writes; permanent search history remains.

### 3. Claim an account and restore its session

[fleet/fleet.go](../internal/fleet/fleet.go), claim, excludes disabled, incomplete,
cooling and attention-paused accounts. It prefers ready snapshots, then the least
recently used account. An OS account lock and an in-process busy flag prevent
concurrent mutations of one account. When eligible accounts are temporarily busy
or cooling, claim waits within the capacity deadline.

[login.go](../internal/providers/eva/login.go), withSession and ensureLocked,
restore portable cookies, browser identity and the original sticky proxy. The
provider verifies egress IP and member authentication. A retained exclusive client
may reuse member verification for two minutes; five minutes idle, changed state,
failure or shutdown discards it. Prepare always verifies. Session expiry is the
earlier of EVA's cookie deadline and configured age, minus five minutes; default
maximum age is three hours. Renewal follows the login/OTP rules below.

### 4. Check actual-party award inventory

[search.go](../internal/providers/eva/search.go), Search, selects
searchAwardWithTaxes for award mode. [award_search.go](../internal/providers/eva/award_search.go)
then calls [award_party_evidence.go](../internal/providers/eva/award_party_evidence.go),
loadAwardPartyAvailability:

1. GET the weekly award-availability form and prepare its current security state.
2. POST the requested route, date, cabin and actual adult count with fresh hidden fields.
3. Confirm the echoed query and parse the requested date's availability icons.

Only positive availability qualifies. Waiting and known unavailable entries are
excluded. Unfamiliar status data remains a diagnosed protocol error rather than
being treated as available or silently hidden. Recognized empty availability is a
complete result and skips mileage and tax quotation entirely.

`availableFor` records the checked party size, not an exact remaining-seat count.
The availability check does not create or confirm a reservation.

### 5. Discover exact bookable itineraries

[award_itinerary.go](../internal/providers/eva/award_itinerary.go), loadAwardJourneys,
opens select-itinerary.aspx, supplies route/cabin/date using EVA's airport metadata,
and follows its fresh Parameter handoff to select-first-journey.aspx.
[award_pricing.go](../internal/providers/eva/award_pricing.go) matches positive
inventory to full flight segments, dates, airports, actual leg cabins and statuses.
Airport changes remain visible with a self-transfer warning. Mixed-cabin journeys
retain the cabin of each leg instead of pretending every leg has the requested cabin.

Public airport metadata is retained per provider for 24 hours. Cookies, form fields,
ZIPSTATE and handoff values remain account/session specific.

### 6. Quote independent itineraries in parallel when capacity allows

[award_parallel.go](../internal/providers/eva/award_parallel.go), priceAwardWithWorkers,
partitions unpriced full itineraries between the primary account and up to two
helpers. [fleet/award_helpers.go](../internal/fleet/award_helpers.go) reserves only
idle ready accounts within the shared capacity ceiling. It never waits for nested
helper capacity: without spare accounts, pricing continues sequentially.

Each helper owns another account, sticky proxy, cookie jar, forms and HTTP client.
It calls SearchAwardQuotes directly, without result-cache writes, tax enrichment,
recursive fan-out or fresh login. A missing authenticated award session returns
control to primary-account fallback.

The primary creates private immutable party-inventory evidence only after its fresh
availability parse and exact choice validation. A helper may skip repeating that
availability request when the normalized query, passenger count and every full
assigned target match and the evidence is no more than 60 seconds old. Its timestamp
is separate from recovery-quote age. Helpers still load fresh booking choices and
validate each selection and mileage response. No cookies or opaque form state are
shared. Missing, stale, future or mismatched evidence uses the full availability
path. One safe initial direct-setup failure may disable evidence and restart that
path, within the same helper deadline and two-attempt limit.

Each helper gets 45 seconds setup plus 35 seconds per assigned itinerary, capped
at three minutes. Its budget also reserves primary execution time for missing
quotations. Reservations are released on completion, timeout, cancellation or panic.
Only exact validated helper fares are merged; missing quotes continue on the primary.
Helper failures and fallback appear in recovery diagnostics.

### 7. Submit each mileage quotation with fresh state

For every actual selection,
[award_selection_security.go](../internal/providers/eva/award_selection_security.go)
prepares three fresh Akamai sensor rounds against that page. Displaying a page or
reusing an already validated recovery quote does not spend those selection rounds.
The provider POSTs the chosen flight, follows its Parameter bridge to
mileage-deducted.aspx and verifies that the returned full itinerary still matches.
It parses the per-person mileage and multiplies by adult count using integer arithmetic.
It stops before passenger information or a reservation submission.

[award_journey_refresh.go](../internal/providers/eva/award_journey_refresh.go)
retrieves fresh selection state for the next quote. Once reopening the journey page
proves that EVA discarded the query, later quotes directly rebuild the itinerary;
they do not repeatedly issue the same unsuccessful reopen request.

Fleet award failover may try up to five different primary accounts for recognized
safe failures, retaining the strongest validated partial result. Unknown credential
or OTP outcomes are excluded. [award_resume.go](../internal/providers/eva/award_resume.go)
reuses an exact quote from this search's recovery context only after fresh party
inventory and full choice matching, with a five-minute original age limit. It never
copies mileage merely because another flight has the same route. Failed or
incomplete searches are not cached.

### 8. Enrich with cash-derived tax estimates

[award_taxes.go](../internal/providers/eva/award_taxes.go) optionally opens one
separate cash booking context on the primary saved session after complete award
mileage retrieval. Insufficient award balance prevents obtaining verified award
taxes; this enrichment does not relabel cash charges as an actual award-tax quote.
It has a two-minute budget and at most one recognized safe restart. Failure retains
verified award results and completed estimates.

[award_tax_match.go](../internal/providers/eva/award_tax_match.go) requires the same
query, party, full flights, airports, times and cabin. Mixed-cabin awards currently
receive no estimate. [award_tax_availability.go](../internal/providers/eva/award_tax_availability.go)
first reconciles each matching cash recommendation's total, base and tax values,
family, flight association, currency and per-adult units. Supported bulk availability
can supply all estimates from one cash response, without per-flight FARE selections
or refresh calls. These estimates have source `eva_cash_availability` and no invented
itemized components.

When those checks cannot establish an estimate, a fresh FARE selection may expose an
itemized breakdown. [award_tax_parse.go](../internal/providers/eva/award_tax_parse.go)
checks selected flights and requires the component sum to equal the per-adult and
whole-party totals. These estimates have source `eva_cash_fare`. Later selections
refresh rotated booking state. Both paths exclude the base fare and return party
totals; neither reaches traveller/catalogue/BOOK, payment or ticketing steps.
Verified taxes and cash-derived `taxEstimate` remain separate fields.

### 9. Convert displayed taxes to USD

[exchange/exchange.go](../internal/exchange/exchange.go) converts original tax or
estimate totals with exact rational arithmetic and rounds USD to two decimals.
The conversion retains original currency, units per USD, rate source and date.
One service shared across the fleet caches validated daily rates in SQLite and
refreshes after the feed's next-update time. It rejects invalid or stale feeds.

A missing conversion does not erase the original airline amount. Conversion has a
seven-second enrichment budget; rate HTTP requests allow six seconds, and failed
refreshes back off for 30 seconds across concurrent callers. Rates use a direct
public request without airline cookies or proxy credentials. Cached rates do not
require a network request for each flight or search.

### 10. Persist, render and follow recovery

The job stores result/error, timestamps, stage timeline and recovery events. A
single history writer coalesces snapshots outside the scheduler lock, retains
failed writes for retry and flushes on shutdown. Admission pauses during storage
failure or an overloaded pending writer. All unfinished records are marked
interrupted after restart, including records beyond the recent-history window.

The browser polls every 1.8 seconds, or five seconds after a connection error.
ETag/If-None-Match avoids retransmitting unchanged results. Preview snapshots are
copied so later mutations cannot alter data already being rendered. Completed,
failed, cancelled and interrupted states stop polling. A failed later quotation
can still leave clearly marked validated partial results.

Identical active queries share one job. **Stop following search** detaches the
browser without cancelling another visitor's shared work. The API cancellation
endpoint still cancels its job globally. A recovered error within the same job
counts as a completed user-visible search; a separate retry job does not rewrite
its predecessor's status.

### Secondary Miles & Cash flow

An empty query mode selects the retained original implementation in
[search.go](../internal/providers/eva/search.go), searchLive. It opens the cash
booking form, solves reCAPTCHA and Akamai, follows the Imperva-protected handoff,
and parses paid fare families. Explicitly Miles & Cash-ineligible fares are removed.

[pricing_plan.go](../internal/providers/eva/pricing_plan.go) deduplicates QuoteID
groups. [pricing.go](../internal/providers/eva/pricing.go) prices at most 30 groups,
refreshing rotated availability and matching full flight/fare/party cash values.
[checkout.go](../internal/providers/eva/checkout.go) performs traveller, dynamic
catalogue and checkout-price preparation with ACTION=BOOK, without payment or
ticketing. Up to two safe replans preserve unchanged quotes; a lost BOOK response
is not blindly replayed. Fleet failover is limited to three accounts and stops
once partial itineraries exist.

The resulting `milesCash` maximum offset and remaining cash are party totals and
are explicitly estimates against paid fares. The remaining cash is not taxes alone,
and paid-fare seat inventory is not award inventory. Account balance/selectability
remains metadata rather than suppressing theoretical maximum-offset estimates.

## Login and OTP

[login.go](../internal/providers/eva/login.go), ensureLocked, fresh-login branch:

1. Acquire the next available sticky pool template and generate a random locked
   session ID. Verify egress through ipify using that client/proxy.
2. Fetch the member login page and parse its fresh ASP.NET hidden state.
3. Solve its current reCAPTCHA v3; solve an image CAPTCHA if the page requires it.
4. Prepare the Akamai sensor sequence for the current page.
5. Connect to Outlook and record mailbox UID validity/next UID/time before asking
   EVA to send verification.
6. Populate account/password and the actual login event, then submit credentials
   once in this attempt.
7. If EVA returns a recognized security continuation, finish that continuation.
8. If an OTP form appears, parse its new hidden state, solve its CAPTCHA if needed,
   wait for the fresh matching email, and submit the current OTP once.
9. Require the authenticated member page, mark verification time and save scoped
   cookies/browser/proxy state. A successful HTTP response alone is insufficient.

[outlook/otp.go](../internal/outlook/otp.go) refreshes OAuth access and connects
using IMAP XOAUTH2. It opens read-only mailboxes and uses BODY.PEEK. Login OTP
polling runs every three seconds for at most 170 seconds, accepting a matching
recipient/sender/subject and new UID with a code less than three minutes old.
It does not use old messages, send mail or mark messages read.

[outlook/retry.go](../internal/outlook/retry.go) retries transient mailbox reads
and temporary OAuth service failures within that same deadline, with delays of
3, 6, 12 and then 15 seconds. Permanent authentication, consent or mailbox-identity
failures stop. These retries never submit EVA credentials or OTP again.

[outlook/token_store.go](../internal/outlook/token_store.go) persists rotated
refresh tokens per mailbox/client, with an exclusive lock. A cached IMAP access
token rejection triggers one forced refresh/new connection; a second failure stops.
An ordinary mailbox password is accepted in bulk input but is not stored or used.

## Background preparation and automatic retries

Read [fleet/recovery.go](../internal/fleet/recovery.go),
[prepare.go](../internal/fleet/prepare.go) and fleet.Start/maintain/outcome.

- Enabled upkeep ticks every 30 seconds. Missing/expired snapshots are selected
  before healthy verification, then oldest check time first.
- A ready idle account is due for verification after five minutes.
- Background concurrency is max(1, min(8, floor(shared limit / 2))).
- Failed account operations 1–14 schedule the next check 15–30 minutes later.
  The stable account/attempt spread avoids retrying the whole fleet together.
- A longer server Retry-After wins. The account sleeps in SQLite, not on a worker.
- The fifteenth consecutive failure sets NeedsAttention. Success clears the budget.
- Manual Prepare re-arms an exhausted account; queued/busy work is deduplicated.
- Cancellation preserves prior recovery metadata rather than penalizing an account.
- Mileage-pricing and recognized award query/protocol errors do not consume the
  account failure budget. Helper not-ready and speculative timeout outcomes also
  do not establish a bad login. Actual authentication/transport failures still do.
- Counters/deadlines survive restart. Old permanent attention flags are converted
  to delayed recovery; interrupted running work gets a new delayed check.
- Fresh failed logins do not save a member snapshot. The next scheduled fresh
  login acquires a new locked identity. Saved-session IP failures renew before
  credentials where safe. HTTP 403-denied leases are quarantined for one minute.

This is not a guarantee that a provider ban, bad password or invalid mailbox token
will fix itself. It ensures transient failures are not permanent manual pauses.

## Bulk onboarding

[mailboxes.go](../internal/registration/mailboxes.go): ParseMailboxes validates input.
[service.go](../internal/registration/service.go): Import saves the batch, then
[workers.go](../internal/registration/workers.go): worker claims a queued row and
calls run -> create in service.go.
[providers/eva/registration.go](../internal/providers/eva/registration.go):
NewRegistrationProfile -> Register.

Each line has email:password:refresh_token:client_id. The service validates and
deduplicates identities, generates a profile once, and persists a private request.
Each worker leases a separate proxy and consumes the shared concurrency budget.
Idle workers wait for import/retry/limit-change notifications instead of querying
SQLite every second. A 15-second upkeep tick wakes them as a fallback after
temporary database errors. Claiming a row is serialized, so only one worker starts
each signup. Workers above a lowered concurrency setting wait for capacity.
It verifies mailbox access before submission, then uses the fresh registration
form/CAPTCHA and generated profile.

RegistrationState persists profile, cookies, proxy, mailbox checkpoint and phase.
Important phases include submit_pending, challenge_pending, await_email,
verification_pending, email_login_pending and created. Pending mutations are
saved before sending. The done container must confirm the registration step;
a generic HTTP 200 or empty page is not sufficient acknowledgement.

[outlook/registration.go](../internal/outlook/registration.go) checks matching
verification emails in inbox/junk, every three seconds for up to five minutes,
within the original one-hour verification window. It extracts the allowed
verification link and OTP. After confirmation and email-login activation, create
stores an account and delegates reusable-session preparation to the fleet.

Retry/continue resumes the checkpoint. It never silently changes the generated
password or submits a second membership after an uncertain submission. An expired
unconfirmed signup needs explicit inspect/restart, which archives the prior
attempt. Created accounts enter normal fleet auto-recovery; incomplete memberships
remain a separate onboarding state. Refreshing or switching pages only hides the
current batch table, not its persistent checkpoints.

## Security adapters retained for EVA

- [sensor.go](../internal/antibot/sensor.go): fresh Akamai script/cookies, up to
  three sequential sensor generations, current-cookie/context propagation.
- [sbsd.go](../internal/antibot/sbsd.go): detected SBSD continuation, bounded by
  its passive/hard variant. It remains reachable from EVA sensor resolution.
- [sec_cpt.go](../internal/antibot/sec_cpt.go): adaptive challenge and mandatory
  provider wait, followed by application verification.
- [reese.go](../internal/antibot/reese.go) /
  [reese_anysolver.go](../internal/antibot/reese_anysolver.go): Imperva/Reese84.
- [recaptcha.go](../internal/antibot/recaptcha.go), [captcha.go](../internal/antibot/captcha.go),
  [text.go](../internal/antibot/text.go): reCAPTCHA v3, extra booking hCaptcha,
  image text recognition and shared task polling.

auto Imperva mode falls back only on generation-service failures. Airline denials,
cancellation, unknown pages and uncertain outcomes are not reasons to blindly
resubmit with a different solver. Solvers are external paid dependencies.

## HTTP, persistence and admin rules

### Deadlines and bounded retry reference

Outer cancellation/deadlines always win over the smaller budgets below. These
limits are layered; they are not additive promises of total execution time.

| Layer | Current rule | Source |
| --- | --- | --- |
| Search capacity wait | 30 minutes before account acquisition | jobs/queue.go, expire |
| Search execution | 15 minutes from first account acquisition; shorter provider budgets still apply | jobs/queue.go, started; fleet/fleet.go, Search |
| Fleet preparation | 6 minutes after account claim | fleet/fleet.go, Prepare |
| Provider session acquisition/preparation | 6 minutes for prepare; 10 for search setup; outer search execution budget still applies | eva/login.go, withSession |
| Registration worker | 15 minutes | registration/service.go, run |
| Airline HTTP | Normally 40 seconds; selected pricing calls 90; 180 requests/operation | session/client.go; eva/pricing.go |
| Transport GET retry | At most one retry, two-second pause | session/client.go |
| Generic transport POST retry | None | session/client.go |
| Saved-member verification recovery | One repeated read, then bounded session renewal | eva/session_verification.go; eva/login.go |
| Search setup HTTP 403 | At most two restarts of recognized pre-pricing setup | eva/handoff.go, retrySearchPreparation |
| Imperva continuation | At most two supported continuation solves | eva/handoff.go, completeBookingSecurity |
| Explicit wrong image CAPTCHA | One fresh verification retry | eva/login.go, sessionAttempts |
| Pricing replanning | At most two refresh/replan recoveries | eva/pricing.go |
| Fleet failover | Award: at most five distinct primary accounts; Miles & Cash: three; stage/uncertainty restrictions apply | fleet/fleet.go; fleet/award_recovery.go |
| Award helpers | Up to two additional ready accounts, only spare shared capacity; 45s + 35s/assigned flight, capped at 3m, with parent fallback reserve | fleet/award_helpers.go |
| Helper inventory evidence | Same query, party and full targets; at most 60s old; missing/invalid evidence uses a fresh availability request | eva/award_party_evidence.go |
| Award setup retry | At most two attempts total; inventory change or safe direct-helper setup fallback; no deadline extension | eva/award_attempts.go |
| Award tax estimates | Optional two-minute enrichment context, at most one safe restart; completed estimates retained | eva/award_taxes.go |
| USD conversion | Seven-second enrichment context; six-second HTTP timeout; shared validated rate cache and 30s failed-refresh backoff | eva/award_taxes.go; exchange/exchange.go |
| Akamai sensors | Up to three sequential rounds; booking requires three | antibot/sensor.go; eva/search.go |
| SBSD | Two passive rounds or one hard round | antibot/sbsd.go |
| reCAPTCHA v3 | 150 seconds, three-second task polls | antibot/recaptcha.go |
| Booking hCaptcha | Four minutes, five-second task polls | antibot/captcha.go |
| Image recognition | 60 seconds, two-second task polls | antibot/text.go |
| Hyper Reese84 | 90-second generation context | antibot/reese.go |
| AnySolver Reese84 | 120 seconds, two-second task polls | antibot/reese_anysolver.go |
| Login email OTP | 170-second wait, three-second polls, three-minute code age | outlook/otp.go |
| Registration email | Five-minute wait, three-second polls, one-hour original verification window | outlook/registration.go |
| Transient mailbox reads | Retry after 3s, 6s, 12s, then 15s, inside the existing email deadline; auth/identity failures stop | outlook/retry.go |
| Delayed account recovery | 15–30 minutes, longer Retry-After honored, pause after 15 consecutive failures | fleet/recovery.go; fleet/fleet.go |

The proxy pool wakes saved-identity waiters on release, with a one-second fallback.
Database proxy-list reads are cached for one second; Admin edits invalidate them
immediately. Fresh allocation prefers an unused non-quarantined template. Each generated
sticky ID contains ten random hexadecimal characters; lockedURL removes ordinary
session/lifetime modifiers so a provider cannot silently swap a failed exit.

[session/client.go](../internal/session/client.go):
HTTPS and allowed-host checks, scoped cookies, immutable browser identity,
8 MiB response bounds and 180 airline requests per operation. GET may retry once
with a two-second pause; generic POST replay is disabled. Ordinary requests are
bounded to 40 seconds; selected pricing calls allow 90 seconds.

[storage/store.go](../internal/storage/store.go): pure-Go SQLite, WAL, four
connections and a five-second busy timeout. Queries/write transactions must finish
before external network waits. Historical jobs are permanent; expired temporary
cache removal does not delete history. Jobs keep a bounded hot in-memory subset;
SQLite is the durable record.

[web/admin.go](../internal/web/admin.go): password hashing, cookie authentication,
12-hour sessions, request-token/origin checks and sign-in throttling. The default
admin password is admin1234. Account/solver secrets are editable only through
authenticated admin responses and masked inputs with reveal controls; they are
not exposed by public configuration/job results.

Follow its routes into [admin_accounts.go](../internal/web/admin_accounts.go),
[admin_settings.go](../internal/web/admin_settings.go) and
[admin_overview.go](../internal/web/admin_overview.go) for account edits, settings
validation and dashboard assembly. Each file owns one feature; access checks stay
at the common route boundary.

The database holds plaintext reusable secrets, not an encrypted secret vault.
The app binds locally. [Demo mode](demo.md) permits a single password-protected
HTTPS hostname through ngrok while admin stays local. `web/demo.go` handles that
boundary. A public multi-user deployment still requires the access-control and
deployment work in [capacity/scaling](capacity.md).

## Tests to read and debugging checklist

- [award_test.go](../internal/providers/eva/award_test.go),
  [award_party_evidence_test.go](../internal/providers/eva/award_party_evidence_test.go)
  and [award_attempts_test.go](../internal/providers/eva/award_attempts_test.go):
  party inventory, exact mileage, evidence boundaries and fresh setup fallback.
- [award_parallel_test.go](../internal/providers/eva/award_parallel_test.go),
  [fleet/award_helpers_test.go](../internal/fleet/award_helpers_test.go) and
  [award_helper_budget_test.go](../internal/fleet/award_helper_budget_test.go):
  disjoint work, account/capacity ownership, merging, fallback and cancellation.
- [award_tax_availability_test.go](../internal/providers/eva/award_tax_availability_test.go),
  [award_tax_flow_test.go](../internal/providers/eva/award_tax_flow_test.go) and
  [exchange_test.go](../internal/exchange/exchange_test.go): bulk/itemized tax
  reconciliation, estimate provenance, exact conversion and shared rate caching.
- [fleet/recovery_test.go](../internal/fleet/recovery_test.go): delay, 15-failure
  budget, restart migration, cooldown eligibility and provider pauses.
- [fleet/fleet_test.go](../internal/fleet/fleet_test.go): exclusive accounts,
  concurrency, failover and cancellation.
- [group_pricing_test.go](../internal/providers/eva/group_pricing_test.go):
  party totals, traveller counts, exact cash comparison and real price changes.
- [pricing_recovery_test.go](../internal/providers/eva/pricing_recovery_test.go)
  and [refresh_test.go](../internal/providers/eva/refresh_test.go): safe replanning.
- [registration_test.go](../internal/providers/eva/registration_test.go) and
  [registration/service_test.go](../internal/registration/service_test.go):
  signup acknowledgement, checkpoints and duplicate-submission protection.
- [storage/settings_test.go](../internal/storage/settings_test.go): obsolete
  configuration cleanup without losing active state/history.
- [tests/browser/http.test.cjs](../tests/browser/http.test.cjs):
  request-token retry boundaries.

For HTTP/admin and job/SQLite integration scenarios, read
[tests/integration](../tests/integration/). Private parsing/recovery unit tests
remain with their Go package. [The test guide](../tests/README.md) explains how to
run each group without live airline or solver calls.

When debugging a slow/failed search, inspect its terminal code, stage, timeline
and recoveries before changing retry limits. Separate dispatch wait, account wait,
sticky-identity wait, security, availability and per-fare pricing. Never print raw
cookies, mailbox tokens, proxy passwords, forms or credential payloads.
