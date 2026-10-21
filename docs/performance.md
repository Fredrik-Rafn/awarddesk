# EVA search speed

Complete EVA results are stored in SQLite for **30 minutes from availability retrieval**.
Change `EVA_RESULT_CACHE_TTL` in Admin; `0s` disables caching, up to `24h` is accepted.
Only complete successes are cached, keyed by account and query. Expiry is checked
on every read; periodic cleanup bounds storage at 1,000 entries.

The frontend labels results **Live** or **Cached**, with their original retrieval
time. **Refresh live (skip recent results)** bypasses cached results and updates
the cache after success. The API equivalent is `"refresh": true` in the normal
search JSON. Responses include `cached` and `cacheExpiresAt` where applicable.
Recent cache hits bypass the live-search queue and provider cooldowns.

Identical queued/running requests share one job, preventing duplicate solver and
airline requests. The live-refresh flag must also match. This is single-process
coalescing; cancelling a shared job cancels it for all callers using that ID.
Different accounts can search concurrently; each individual account remains exclusive.
The default concurrency limit is four, with up to 16 configurable in Admin.

For uncached searches, the external CAPTCHA task runs alongside the full Akamai
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
