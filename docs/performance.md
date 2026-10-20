# EVA search speed

The app persists complete search results as JSON in `.state/eva-results/`.
The default lifetime is **two minutes from availability retrieval**, not from
the last cache hit. Set `EVA_RESULT_CACHE_TTL=2m` in `.env` to change it; `0s`
disables the cache and the maximum accepted value is `15m`.

The key includes the account configuration, provider, route, date, cabin and
adult count. Only complete successful results are cached, including confirmed
empty results. Errors, partial results, expired files, future timestamps and
unrecognized file versions are never reused. At most 100 result files are kept.
Cache writes are atomic; corrupt or unreadable cache files fall back to live
searches. Cookies, page tickets, traveller profiles and booking tokens are not
stored in these result files.

The frontend labels results **Live** or **Cached**, with their original retrieval
time. **Refresh live (skip recent results)** bypasses cached results and updates
the cache after success. The API equivalent is `"refresh": true` in the normal
search JSON. Responses include `cached` and `cacheExpiresAt` where applicable.
Recent cache hits bypass the live-search queue and provider cooldowns.

Identical queued/running requests share one job, preventing duplicate solver and
airline requests. The live-refresh flag must also match. This is single-process
coalescing; cancelling a shared job cancels it for all callers using that ID.
Different live searches still use the account sequentially.

For uncached searches, the external CAPTCHA task runs alongside the full Akamai
sensor sequence. Only the sensor task uses the airline HTTP client; CAPTCHA
verification and availability wait for both tasks. A failure cancels the other
task. There are still three required sensor rounds and no fixed request spacing.
Booking and pricing steps continue in order because page tickets, recommendation
IDs and booking session state change after each request.

Stage and pricing-operation timings are logged without credentials or payloads.
Use these logs to identify future bottlenecks rather than removing required
steps speculatively.

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
