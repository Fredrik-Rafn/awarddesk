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
7. [eva/search.go](../internal/providers/eva/search.go): follow searchLive.
8. [eva/pricing.go](../internal/providers/eva/pricing.go) and
   [checkout.go](../internal/providers/eva/checkout.go): follow one fare quote.
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

### 1. Browser submission

[app.js](../internal/web/assets/app.js), the search form submit handler, builds a
query containing provider=eva, origin, destination, date, cabin and adult count.
[http.js](../internal/web/assets/http.js) sends JSON with the current request token.

The normal UI uses the result cache. The API can set refresh=true for a live
request. Validation requires different three-letter airports, a date from today
through one year ahead, supported cabin and 1–9 adults. Only one-way is implemented.
The airport directory supplies suggestions; it does not prove route service.

### 2. Local HTTP boundary

[server.go](../internal/web/server.go), Handler and submit, validates Host, Origin,
request token, JSON shape and query. It passes the request to jobs.Submit and
returns a job ID; the browser does not hold an HTTP request open for all pricing.

A stale token gets one explicit token-refresh retry in the browser only when
middleware rejected it before the action. An ordinary timeout is not proof a
mutation failed, so it is not automatically replayed.

### 3. Deduplication, cache and queue

[jobs/service.go](../internal/jobs/service.go), Submit:

- Coalesce an identical queued/running Query, including its refresh flag.
- Ask the fleet for a complete valid cache result before entering live queues.
- Otherwise create a cancellable job with a 15-minute overall deadline.
- Queue into a bounded channel of 1,024 entries; an in-memory item bound of 1,000
  and cleanup of old finished items also apply. This is not an unlimited queue.
- Workers call execute, timestamp start, publish progress and persist snapshots.

Cache identity includes account configuration and query, with refresh normalized
away for the cache key. A fleet cache lookup visits configured accounts. Default
TTL is 30 minutes from availability retrieval, not from final pricing completion.
Only complete successful results are cached, including recognized empty results.
Failed/partial results are not cached. SQLite cache cleanup limits entries to 1,000.

Read [result_cache.go](../internal/providers/eva/result_cache.go),
[cache_policy.go](../internal/storage/cache_policy.go) and fleet.CachedSearch.
Disabling caching clears temporary results immediately and blocks late cache
writes from in-flight searches; permanent job history remains.

### 4. Choose and lease an account

[fleet.go](../internal/fleet/fleet.go), Search and claim:

1. Exclude disabled, incompletely configured, cooling or attention-paused accounts.
2. Respect the shared active-operation ceiling, including registration leases.
3. Prefer a locally valid saved member snapshot.
4. Among equally ready candidates, prefer the account least recently used.
5. Acquire the account's OS state lock and mark its in-process entry busy.
6. Keep ownership through the entire search; release exactly once.

When all eligible accounts are busy, claim polls every 150 ms within the job's
deadline. If no account is eligible, it returns accounts_unavailable. It does not
queue indefinitely waiting for a 15-minute cooldown to expire.

Up to three different accounts may be tried for recognized safe preparation
failures. safeFailover excludes uncertain outcomes, credential/OTP/pricing stages,
and failover stops once partial itineraries exist. Account switching is not a
universal retry of every failed search.

### 5. Restore or create member login

Provider.Search wraps cache handling and calls searchLive.
searchLive uses [login.go](../internal/providers/eva/login.go), withSession:

1. Apply the operation deadline: six minutes for preparation, ten for search.
2. Acquire the provider gate and logical snapshot OS lock.
3. Load/validate the portable saved session, configuration identity and expiry.
4. Resume the original sticky proxy and restore scoped cookies/browser identity.
5. Verify egress IP and the authenticated member page.
6. If a recognized expiry/proxy failure permits renewal, discard the old snapshot
   and create a new login. See the next section for credentials and OTP.

[expiry.go](../internal/providers/eva/expiry.go) calculates expiry as
min(EVAID cookie deadline, creation + configured age) minus five minutes.
Default age is three hours. The server may revoke a login earlier.

[warm_session.go](../internal/providers/eva/warm_session.go) retains one exclusive
HTTP client. Changed state, failure, more than five minutes idle or shutdown
discards it. Searches may reuse a successful member verification for two minutes;
explicit Prepare always checks. Neither optimization extends session expiry.

### 6. Create a booking context

[search.go](../internal/providers/eva/search.go), searchLive, opens BookingURL,
handles a recognized member-login redirect/adaptive page, parses the search form,
and prepares fresh form fields. A saved member login does not mean the previous
booking cart can be reused.

[verification.go](../internal/providers/eva/verification.go) runs independent
reCAPTCHA solving and Akamai preparation concurrently. It waits for both before
submitting availability. The same airline HTTP client is not used by two
concurrent state-changing sequences.

The booking handoff carries fresh opaque fields, not hardcoded session tickets.
[handoff.go](../internal/providers/eva/handoff.go) validates/forwards the expected
flow. Imperva/Reese84 may interrupt it; Hyper is preferred in auto mode and
AnySolver is a fallback for solver-generation failures. Additional hCaptcha and
Akamai adaptive continuations are retained because EVA uses them.

### 7. Parse availability

[parse.go](../internal/providers/eva/parse.go), decodePage and parseAvailability,
extract page tickets, booking-session identifiers, recommendations, segments,
fare families, cash totals and eligibility from the supported response shapes.

Availability is not yet a mileage quote. [money.go](../internal/providers/eva/money.go)
uses exact decimal arithmetic to normalize per-adult cash to the whole party.
An explicitly ineligible Miles & Cash fare is removed; a flight with no remaining
eligible fares is removed too. Unknown legacy eligibility is not fabricated.

Progress publishes an isolated preview, so later provider mutations cannot change
a result the UI is already reading. [award/snapshot.go](../internal/award/snapshot.go)
copies nested pointers and slices.

### 8. Price each distinct fare group

[pricing.go](../internal/providers/eva/pricing.go), priceFaresWithReplans:

1. Count unique QuoteID groups still needing mileage values. Shared recommendations
   are priced once and applied to all matching fares.
2. Enforce the bounded limit of 30 groups. Larger sets remain incomplete with a
   warning; they are not cached as complete.
3. For later groups, refresh availability because booking state rotated during
   the previous quote. Match the same flight/fare against the fresh response.
4. Submit fare selection with fresh recommendation/flight IDs and PAGE_TICKET.
5. Require the selected price context to match currency, mode and normalized
   whole-party cash price. A true changed price still fails this check.
6. Call checkoutQuote, which performs the ordered sequence below.
7. Parse maxRankAvailable and distribute the quote to every fare sharing QuoteID.
8. Publish completed-quote previews and continue.

[checkout.go](../internal/providers/eva/checkout.go), checkoutQuote:
travellers -> dynamic catalogue -> checkout price preparation.
[pricing.go](../internal/providers/eva/pricing.go), quoteRequest, maps those stages
to AddElementsTravellerInformation, AddElementsDynamicCatalog and
AddDynamicCatalogSelectionToTripPlan. The final payload uses ACTION=BOOK,
TRANSACTION_ID=RequestAdditionalInformation and CONFIRM_SERVICE_SELECTION=TRUE.
No payment or ticketing endpoint is permitted by this adapter.

[refresh.go](../internal/providers/eva/refresh.go) refreshes and matches selections,
carrying one ADT traveller entry per adult. At most two pricing replans recover
recognized availability/state changes, preserving unchanged quotes. A lost BOOK
response is not blindly resubmitted.

parseMilesCash reads the theoretical maximum offset, not merely slider positions
selectable by the account's current balance. Selectable remains metadata;
aggregation does not hide a maximum estimate because the test account lacks miles.

### 9. Complete, persist and render

The job records final Result/Error, timestamps, stage timeline and recoveries.
[storage/analytics.go](../internal/storage/analytics.go) computes retained history
statistics. The browser polls active job status every 1.8 seconds (five seconds
after a connection error), replaces previews
with the final result and offers filters/export. Cancellation applies to the
shared job ID, so all callers coalesced to it see that cancellation.

A valid empty result is a completed job; a protocol/security error is a failure.
A recovered error inside the same job still yields a user-visible success.
A separate later job does not rewrite the earlier failure.

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
- Mileage-pricing errors leave login usable and do not consume this failure budget.
- Counters/deadlines survive restart. Old permanent attention flags are converted
  to delayed recovery; interrupted running work gets a new delayed check.
- Fresh failed logins do not save a member snapshot. The next scheduled fresh
  login acquires a new locked identity. Saved-session IP failures renew before
  credentials where safe. HTTP 403-denied leases are quarantined for one minute.

This is not a guarantee that a provider ban, bad password or invalid mailbox token
will fix itself. It ensures transient failures are not permanent manual pauses.

## Bulk onboarding

[registration/service.go](../internal/registration/service.go):
ParseMailboxes -> Import -> worker -> run -> create.
[providers/eva/registration.go](../internal/providers/eva/registration.go):
NewRegistrationProfile -> Register.

Each line has email:password:refresh_token:client_id. The service validates and
deduplicates identities, generates a profile once, and persists a private request.
Each worker leases a separate proxy and consumes the shared concurrency budget.
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
| Search job | 15 minutes including queue wait | jobs/service.go, Submit |
| Fleet preparation | 6 minutes after account claim | fleet/fleet.go, Prepare |
| Provider session operation | 6 minutes for prepare; 10 for search | eva/login.go, withSession |
| Registration worker | 15 minutes | registration/service.go, run |
| Airline HTTP | Normally 40 seconds; selected pricing calls 90; 180 requests/operation | session/client.go; eva/pricing.go |
| Transport GET retry | At most one retry, two-second pause | session/client.go |
| Generic transport POST retry | None | session/client.go |
| Saved-member verification recovery | One repeated read, then bounded session renewal | eva/session_verification.go; eva/login.go |
| Search setup HTTP 403 | At most two restarts of recognized pre-pricing setup | eva/handoff.go, retrySearchPreparation |
| Imperva continuation | At most two supported continuation solves | eva/handoff.go, completeBookingSecurity |
| Explicit wrong image CAPTCHA | One fresh verification retry | eva/login.go, sessionAttempts |
| Pricing replanning | At most two refresh/replan recoveries | eva/pricing.go |
| Fleet failover | At most three distinct accounts; stage/uncertainty restrictions apply | fleet/fleet.go |
| Akamai sensors | Up to three sequential rounds; booking requires three | antibot/sensor.go; eva/search.go |
| SBSD | Two passive rounds or one hard round | antibot/sbsd.go |
| reCAPTCHA v3 | 150 seconds, three-second task polls | antibot/recaptcha.go |
| Booking hCaptcha | Four minutes, five-second task polls | antibot/captcha.go |
| Image recognition | 60 seconds, two-second task polls | antibot/text.go |
| Hyper Reese84 | 90-second generation context | antibot/reese.go |
| AnySolver Reese84 | 120 seconds, two-second task polls | antibot/reese_anysolver.go |
| Login email OTP | 170-second wait, three-second polls, three-minute code age | outlook/otp.go |
| Registration email | Five-minute wait, three-second polls, one-hour original verification window | outlook/registration.go |
| Delayed account recovery | 15–30 minutes, longer Retry-After honored, pause after 15 consecutive failures | fleet/recovery.go; fleet/fleet.go |

The proxy pool checks saved-template availability every second. Each generated
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

The database holds plaintext reusable secrets, not an encrypted secret vault.
The app binds locally. Publishing it would require the access-control and
deployment work in [capacity/scaling](capacity.md).

## Tests to read and debugging checklist

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
- [web/testdata/http.test.cjs](../internal/web/testdata/http.test.cjs):
  request-token retry boundaries.

When debugging a slow/failed search, inspect its terminal code, stage, timeline
and recoveries before changing retry limits. Separate dispatch wait, account wait,
proxy-template wait, security, availability and per-fare pricing. Never print raw
cookies, mailbox tokens, proxy passwords, forms or credential payloads.
