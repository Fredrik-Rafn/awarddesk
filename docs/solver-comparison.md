# Incapsula/Reese84 solver comparison

**21 October 2026: use Hyper first, with AnySolver as a service-failure backup.**
Hyper Incapsula access is now working for `book.evaair.com`.

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
