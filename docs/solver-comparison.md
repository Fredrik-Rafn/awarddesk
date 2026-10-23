# Solver comparison and request savings

**21 October 2026: use Hyper first, with AnySolver as a service-failure backup.**
Hyper Incapsula access is working for `book.evaair.com`. The measurements below
are a historical Miles & Cash comparison, not a benchmark of the current primary
award-ticket flow. Current award/helper and tax savings follow the comparison.

## Method

Ten uncached live searches, five per provider, using one verified EVA account
and the same sticky proxy. Routes were HND to SIN in economy, one adult, for
22, 26 and 27 November 2026. Provider order alternated between paired rounds.
The logged-in session and Akamai cookies were retained. Only Incapsula security
cookies were removed before each run to require a fresh solver call. This cookie
removal is a benchmark procedure; normal searches keep and reuse those cookies.

Every scored run invoked its selected Reese84 solver exactly once and completed
flight retrieval and mileage pricing. Result caching was disabled. No scored
run needed login renewal, solver fallback or search-recovery retries, and the
sticky proxy did not change.

## Measurements

| Metric | Hyper | AnySolver |
| --- | ---: | ---: |
| Successful fresh Reese84 verifications | 5/5 | 5/5 |
| Successful complete searches | 5/5 | 5/5 |
| Median Reese84 verification | **2.918 s** | 6.340 s |
| Reese84 verification range | 2.802–4.545 s | 6.243–6.386 s |
| Median whole booking-security stage | **5.804 s** | 9.042 s |
| Median full search | 46.972 s | 46.527 s |
| Full-search range | 42.407–84.003 s | 43.163–84.400 s |
| Search-recovery retries | 0 | 0 |

Reese84 verification includes script retrieval, solver generation and submitting
the sensor to EVA for a token. The whole booking-security stage also includes
EVA's continuation response. Hyper was about **54% faster** for the measured
verification step. AnySolver had less spread in that step, but its fastest
sample was still slower than Hyper's slowest sample.

| Round | November departure | Hyper verification / full search | AnySolver verification / full search |
| --- | --- | ---: | ---: |
| 1 | 22 | 3.205 / 42.407 s | 6.363 / 46.527 s |
| 2 | 27 | 4.545 / 67.785 s | 6.340 / 45.591 s |
| 3 | 26 | 2.806 / 84.003 s | 6.386 / 84.400 s |
| 4 | 27 | 2.802 / 46.972 s | 6.267 / 48.276 s |
| 5 | 22 | 2.918 / 43.309 s | 6.243 / 43.163 s |

There is no demonstrated overall search-time advantage in this small sample.
EVA pricing latency and the number of quotes dominate: round 3 returned six
quotes per provider, while the other rounds returned one or two. Availability
also changed during the tests: round 2 returned one itinerary for Hyper and two
for AnySolver. The solver-step comparison is therefore more useful than the
full-search totals. Five successful samples each do not establish an always-
successful service or a statistically reliable failure rate.

An earlier warm-up failed with an EVA login HTTP 403 before Incapsula was
reached. Session preparation subsequently succeeded. That login failure is
separate from the scored Reese84 tests and is not fixed by choosing a different
Incapsula provider. Preliminary searches that reused an existing security token
were also excluded because they did not invoke a solver.

## Selected configuration

```dotenv
EVA_IMPERVA_SOLVER=auto
```

`auto` uses Hyper first. If Hyper cannot generate the sensor because of an
upstream service failure, it tries AnySolver once for the same fresh security
continuation. Proxy transport failures, airline rejections, changed challenge
formats, cancellation and uncertain outcomes do not trigger this fallback.
The existing bounded session/search recovery still applies to those failures.
Credentials, OTP, availability handoffs and pricing operations are not replayed
by changing solvers.

Use `hyper` or `anysolver` to force a provider for diagnostics. AnySolver remains
configured for the other CAPTCHA types. Both adapters log solver name, success
and duration without keys, cookies or payloads. Automated tests cover fallback
success, backup failure, forbidden fallback cases and cancellation.

After selecting `auto`, one further fresh-challenge live check used Hyper,
completed Reese84 in 2.777 s, and returned both priced itineraries in 42.016 s.
This additional validation is excluded from the five-per-provider table above.

## Request savings and reuse boundaries

### Award quotations and independent helpers

Award tickets are the default UI mode. A primary account checks award inventory
for the actual party and discovers exact bookable itineraries. Up to two spare
ready accounts can quote disjoint itineraries concurrently, each keeping its own
proxy, cookies, ASP.NET state and security sequence. Helpers share the global
concurrency ceiling and never wait for nested capacity or perform fresh login.

[Parent inventory evidence](../internal/providers/eva/award_party_evidence.go)
avoids repeating the same weekly availability request on each helper. The private
evidence is created only after a fresh primary availability parse and choice
validation. Every assigned full itinerary, normalized query and adult count must
match; evidence must be no more than 60 seconds old and cannot be future-dated.
Helpers still load fresh booking choices, verify their statuses and prepare each
actual flight selection independently. No cookies, solver context, ZIPSTATE or
handoff parameters are copied between accounts.

For a normal sensor-only flow, let N be the number of actual mileage selections
across all accounts and H the number of helpers that successfully reuse parent
evidence. The primary inventory check uses three Hyper generations and each
selection uses three more: **3 + 3N**. Repeating availability separately on those
helpers would add **3H** generations. Valid evidence therefore saves three per
helper, at most six for the current two-helper limit. These are conditional code
counts, not a promise about challenges, retries or the final live benchmark.
IP verification requests are proxy traffic but are not paid Hyper generations.

Missing or invalid evidence performs a full fresh helper availability check.
[Bounded setup recovery](../internal/providers/eva/award_attempts.go) may also
disable otherwise-valid evidence and restart after a safe initial direct-setup
failure, before any quote. It uses at most two attempts within the same deadline;
authentication, uncertain outcomes and pricing-stage failures are excluded from
that shortcut. Failed helpers return validated partial quotes and the primary
continues missing work. This recovery can reduce the expected request savings.

Helper time is capped at 45 seconds setup plus 35 seconds per assigned flight,
up to three minutes, with primary execution time reserved for fallback. Exact
mileage is validated independently for each itinerary; equal route/cabin alone
does not authorize copying a quote between flights.

### Selection sensors and existing reuse

Award flight selection now prepares Akamai security immediately before an actual
selection POST. Displaying a journey page alone costs no selection sensor calls.
This saves three Hyper generation requests when all fresh choices can use exact
recent recovery quotes, or when a page is rejected before any selection. Each
flight that is submitted still receives three fresh sensor rounds using that
page's current body and form state. Mocked tests verify these request counts,
page binding and failure handling; this is separate from the live measurements
above.

Existing reuse also avoids work: complete result caching, retained account cookie
jars and transports, exact mileage quote recovery after fresh party availability,
and retained completed tax estimates during recovery. Recovery quotes retain their
original age and expire after five minutes. Identical active API queries already
coalesce through `jobs.Submit` and `inflightLocked`, returning the same job ID.
`VerifyConnection` calls IP verification; it does not call Hyper's paid sensor API.

### Bulk tax estimates

After complete award mileage retrieval, optional tax estimation opens one separate
cash booking context on the primary saved member session. Its security work is
shared across matching flights, rather than repeating cash login/setup per flight.
This can invoke booking reCAPTCHA, Akamai and, when challenged, Reese84; tax lookup
is therefore not a solver-free operation.

[Bulk availability reconciliation](../internal/providers/eva/award_tax_availability.go)
can use the initial cash recommendations' explicit tax amounts for every exact
matching flight. It verifies currency, fare family, flight association, adult
units, total/base/tax arithmetic, zero separately reported service fee and absence
of dynamic discount. These estimates use source `eva_cash_availability`; no
itemized breakdown is invented. When all flights pass, the tax phase needs no
per-flight FARE selection or availability refresh.

When bulk reconciliation is unavailable, the existing FARE path obtains and
reconciles an itemized breakdown for that exact flight, refreshing state between
selections as required. Those estimates use source `eva_cash_fare`. Both remain
cash-derived estimates rather than verified award taxes, and neither path enters
traveller, catalogue, BOOK, payment or ticketing work. Mixed-cabin targets and
unmatched or malformed charges remain unquoted. Tax failures preserve award
mileage and completed estimates.

[USD conversion](../internal/exchange/exchange.go) uses one shared validated rate
cache, persisted in SQLite, and refreshes according to the public feed's next-update
time. It makes no solver calls, sends no airline cookies or proxy credentials and
does not require one rate request per flight. Failed refreshes back off for 30
seconds; unavailable conversion retains the original currency amount. Original
amount, conversion rate, source and date stay available for inspection.

### Identity boundaries

[Hyper's Akamai guide](https://docs.hypersolutions.co/akamai-web/getting-started)
requires consistent IP, User-Agent, TLS identity, headers and cookies. The current
implementation sends the script only for the first sensor and carries returned
context through subsequent rounds. Protected actions can invalidate cookies, so
sensor payloads and context must not be replayed across actions or shared across
accounts, proxies or browser identities. EVA's earlier live failures after
shortened cash search preparation and skipped award selection sensors are why
those protected actions retain three rounds.

[Basic SBSD requires both ordered sensor posts](https://docs.hypersolutions.co/akamai-web/sbsd-introduction).
[Reese84 documentation](https://docs.hypersolutions.co/incapsula/reese84-dynamic)
permits retaining its cookie with the returned scope and expiry while maintaining
the same session identity; the adapters already do this and solve only when a
challenge is encountered. Caching public script bytes or reducing IP-check HTTP
requests would not by itself reduce paid Hyper generation calls.
