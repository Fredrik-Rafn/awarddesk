# EVA search speed

## Award tickets: current path

The final [200-search validation](verification-status.md) completed with no final
failures: 79 priced searches and 121 valid empty results. Priced completion was
60.5 seconds median / 150.2 seconds P95; first mileage was 27.3 seconds median.
Optional tax enrichment was 15.3 seconds median and covered 225 of 242 quotes.
The overall 18.7-second median includes empty inventory and should not be quoted
as the typical time to return priced flights.

| Flights quoted in one search | Searches | Median completion | P95 |
| --- | ---: | ---: | ---: |
| 1 | 22 | 39.8 s | 66.6 s |
| 2–3 | 34 | 58.6 s | 118.0 s |
| 4–6 | 9 | 83.7 s | 170.5 s |
| 7 or more | 14 | 113.5 s | 153.3 s |

These cohorts mix routes, passenger counts, recovery and available helper capacity;
they show the remaining flight-count tendency, not a controlled parallelism
comparison. A pre-change seven-flight TPE–HKG sample took 265.4 seconds. The same
query later took 73.7 seconds in a smoke check and 153.3 seconds in the final run.
Those observations demonstrate variability as well as improvement opportunities;
they do not establish a fixed percentage speedup.

Award tickets are the main UI mode. Availability is checked for the actual party
before any mileage quote. The parent prices its own lane while up to two idle
ready accounts price other exact itineraries, each with an independent session
and proxy. Helpers count against the shared concurrency limit, never wait for a
slot, and never perform a new credential login. Missing helper quotes return to
the parent rather than becoming a partial success.

A helper can reuse the parent's positive availability evidence for at most 60
seconds when query, party and full itineraries match. It still obtains its own
fresh booking forms and selection security. Otherwise it performs availability
normally. This avoids duplicate availability preparation without sharing cookies,
one-use form state or security payloads across accounts.

Tax estimates use a separate cash context. Validated per-adult taxes from one cash
availability response can cover every exact matching flight; ambiguous amounts,
fare families or totals fall back to the selected-fare response. No traveller,
catalogue or checkout step is needed for this estimate. Missing estimates do not
discard successfully quoted award mileage. USD rates are shared and cached daily.

More flights still mean more protected mileage selections. Three fresh Akamai
rounds remain required per actual selection. Parallel helpers reduce elapsed time
when spare accounts are available; they cannot remove this upstream work. See
[award flow](eva-awards.md) for time budgets and validation, and
[verification evidence](verification-status.md) for measured results.

## Shared cache and scheduling

Complete EVA results are stored in SQLite for **30 minutes from availability retrieval**.
Award recovery conservatively retains the oldest reused quote timestamp instead
of extending freshness. The query mode isolates award and Miles & Cash caches;
see [award efficiency](eva-awards.md#efficiency-and-diagnostics) for its separate
request sequence and quote-recovery rules.
Change `EVA_RESULT_CACHE_TTL` in Admin (up to `24h`). Its cache switch disables
caching and clears temporary results after confirmation. A legacy `0s` lifetime
also requires confirmation. Historical searches stay permanently.
Only complete successes are cached, keyed by account and query. Expiry is checked
on every read; periodic cleanup bounds storage at 1,000 entries.

The frontend labels results **Live** or **Cached**, with their original retrieval
time. The API supports `"refresh": true` to bypass cached results and update
the cache after success. Responses include `cached` and `cacheExpiresAt` where applicable.
Recent cache hits bypass the live-search queue and provider cooldowns.

Identical queued/running requests share one job, preventing duplicate solver and
airline requests. The live-refresh flag must also match. This is single-process
coalescing. The browser's Stop following search action detaches that viewer;
it does not cancel work another viewer may be following. Explicit API cancellation
of the shared job affects all callers using that ID.
Different accounts can search concurrently; each individual account remains exclusive.
The default concurrency limit is four, with up to 512 configurable in Admin.

## Miles & Cash: retained secondary path

For uncached Miles & Cash searches, the external CAPTCHA task runs alongside the full Akamai
sensor sequence. Only the sensor task uses the airline HTTP client; CAPTCHA
verification and availability wait for both tasks. A failure cancels the other
task. There are still three required sensor rounds and no fixed request spacing.
Booking and pricing steps continue in order because page tickets, recommendation
IDs and booking session state change after each request.

## Warm connections and progressive results

EVA retains one serialized HTTP client for its current account/proxy session.
Every operation still locks and loads the portable JSON record in SQLite and checks the proxy
exit IP. A changed saved snapshot or proxy, any failed operation, or more than five
minutes idle before reuse discards the warm client. The server closes retained
connections at shutdown. Each operation receives a fresh request budget while
the established HTTP identity remains immutable.

Live searches may reuse a successful member verification for at most two minutes.
This timestamp does not extend on cache hits or subsequent searches. Restarting
the process or replacing session state requires verification again. Explicit
Prepare EVA session checks always verify. A recognized redirect to EVA's member
login before pricing permits the existing single fresh-session renewal; uncertain
credential, OTP and checkout requests are not replayed.

The API publishes isolated partial results while a job is still running: flight
availability first, then completed mileage quotes after each distinct fare.
The frontend labels outstanding calculations, preserves filters and expanded
flight details during updates, and replaces the preview with the final result.
Incomplete snapshots are not written to the complete-result cache.

The EVA diagnostic CLI also records `firstResultsMs` and `firstQuoteMs`, alongside
total duration, so first useful output is measured separately from completion.

Two live uncached checks on 21 October passed with progressive results:

| Route, 30 November 2026, business | First flights | First mileage quotes | Complete |
| --- | ---: | ---: | ---: |
| TPE–HKG, six flights / 18 fare quotes | 32.1 s | 47.1 s | 86.1 s |
| HND–SIN, four flights / four fare quotes | 68.0 s | 84.1 s | 84.1 s |

The second search used the warm transport and recent-verification path. The first
member verification took 4.118 s; the second reuse stage took 3 ms after its normal
proxy check. This confirms the skipped verification, not a paired end-to-end
speedup: the second search's CAPTCHA/security preparation took 59.5 s versus
16.3 s on the first. In Chrome, flights and partial mileage quotes appeared while
the first job was running; airline/sort selections and expanded details survived
subsequent quote updates and completion.

Stage and pricing-operation timings are logged without credentials or payloads.
Use these logs to identify future bottlenecks rather than removing required
steps speculatively.

For fresh Incapsula challenges, the [21 October solver comparison](solver-comparison.md)
selects Hyper first: 2.918 s median verification versus AnySolver's 6.340 s,
with both succeeding in five controlled tests. Full-search timings did not show
a clear overall speedup. Normal searches retain reusable security cookies.

## Live verification, 20 October 2026

HND to SIN, 27 November 2026, economy, one adult, two itineraries:

| Check | Measured time |
| --- | ---: |
| Live search with overlapping security/CAPTCHA | 37.24 s |
| Identical complete result from cache | 6 ms |
| Same cache after restarting the app | 35 ms |
| Frontend Refresh live bypass | 30.54 s |

The cache timings cover local API submission through the completed result,
not browser rendering. The frontend was also checked: cached results appeared
immediately and retained the original retrieval time. A previous uncached run
was also about 37 seconds; these samples do not establish an overall speedup for
unique live searches. Live timing depends on proxies, solver queues and EVA.

In that live run, member verification took 4.1 s, combined booking security and
CAPTCHA 7.0 s, booking handoff/security 8.2 s, and mileage pricing 13.7 s. Pricing
already deduplicates shared recommendations. Reusing a conversion ratio across
different routes/fares, persisting one-use CAPTCHA answers or skipping traveller
and catalog transitions has not been validated and is not implemented.

## Local contention improvements

Concurrency is configurable; the 200-search validation uses at most 30. The FIFO admits up to 1,024 queued
searches independently of finished history. Capacity waiting has 30 minutes,
separate from 15 minutes of execution after first account acquisition. Lowered
limits drain active work; cancellation immediately frees queued capacity.
Release notifications replace tight capacity polling, with a one-second fallback.
Distinct saved sticky IDs no longer serialize their whole proxy template.

Cache/history I/O and SQL/JSON progress writes run outside the job scheduler mutex.
One writer coalesces superseded snapshots and flushes admission before dispatch.
Unchanged polls use ETag/304 without full result cloning/transmission. Small
settings reads omit the large proxy list, and account runtime statuses batch.
These reduce local overhead; dependent EVA pricing calls still dominate latency.
