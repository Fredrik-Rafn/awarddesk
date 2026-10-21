# EVA reliability — 21 October 2026

Live sequential searches through the local website API, one configured EVA account,
sticky residential proxy, Hyper preferred for booking security, AnySolver fallback.
Every query used refresh=true: no result-cache hits. The baseline had normal background
session maintenance enabled; diagnostic and hardened batches disabled maintenance to
isolate each search. Saved member sessions and valid security cookies were reused.
These batches measure warm-session search reliability, not fresh-login reliability
or concurrent multi-account throughput.

## Outcomes

| Batch | Searches | Clean success | Recovered success | Final failure | Partial completion | User success |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Baseline | 24 | 23 | 0 | 1 | 0 | 95.8% |
| Diagnostic repeats | 4 | 3 | 0 | 1 | 0 | 75.0% |
| After fare-change hardening | 8 | 8 | 0 | 0 | 0 | 100.0% |
| Final validation | 4 | 4 | 0 | 0 | 0 | 100.0% |
| All live searches | 40 | 38 | 0 | 2 | 0 | 95.0% |

Separate cold-session preparation: **1/1 success**, 107.3 seconds, with the old local session removed and a fresh sticky proxy acquired. The flow completed browser security, Outlook OTP retrieval/submission and saved a verified new session. No second click or credential retry was needed. Final-validation searches used this newly prepared session. One cold login is too small a sample to establish its long-run success rate.

User success means a completed, complete result from the original submitted job,
including any automatic recoveries. A separate rerun never changes the original
failure. Valid no-flight responses count as successful requests and are also
identified below (2 across these batches). Partial pricing is never a full success.

Clean means no *recorded application recovery*. Recoveries are sanitized events
from request retries, search/session renewal, solver fallback and changed-availability
replanning (0 recorded across these batches). Nested recovery events are not
independent full searches. Solver-internal retries are not exposed by their APIs;
these figures are not a count of every upstream HTTP request or solver attempt.

## Findings and changes

- The baseline failed on JFK–TPE business, 25 November, after the first quote,
  while refreshing availability: search_protocol, “EVA booking data was not found”.
  Both targeted reruns passed but returned fewer fare options. No failed response
  was reproduced with diagnostics; its exact cause remains unconfirmed.
- A diagnostic LHR–TPE business repeat failed when availability or prices changed
  between fare quotes. Recovery now adopts the new availability snapshot at most
  twice. Finished quotes survive only when flight segments, fare name, cabin and
  cash price still match. Fresh IDs and prices come from the new snapshot.
- Truncated GET responses now use the existing single retry, instead of ending
  immediately. Temporary CAPTCHA task-status transport/read/5xx errors retry once
  against the existing task ID. Task creation is not replayed.
- The final version additionally retries the read-only availability-refresh POST
  once for interrupted, missing/malformed or HTTP 5xx responses. It never repeats
  fare selection or checkout. A controlled missing-page response recovers on the
  same job, and a persistently malformed response stops after the second call.
- Failed late pricing retains available flights and finished quotes in the API
  and frontend, explicitly marked partial alongside the final error.
- Search jobs and session preparation retain recovery metadata, allowing clean,
  recovered and final outcomes to be measured after completion.

Controlled tests cover both transient retry paths, non-replay of POST/task creation,
changed-fare replanning without repeating a finished checkout, stale-quote rejection,
partial-result retention and the frontend failure branch. Live samples cannot prove
zero future failures; broader cold-login sampling and concurrent-account loads need
separate measurement. No credentials, OTPs or payment/ticketing requests are replayed by these fixes.

## Per-search measurements

| Batch | # | Route | Departure | Cabin | Outcome | Flights | Quotes | Seconds | Recovery/error |
| --- | ---: | --- | --- | --- | --- | ---: | ---: | ---: | --- |
| Baseline | 1 | HND–SIN | 2026-11-22 | economy | success | 2 | 2 | 44.0 | — |
| Baseline | 2 | TPE–HKG | 2026-11-24 | business | success | 7 | 21 | 76.0 | — |
| Baseline | 3 | TPE–LAX | 2026-11-24 | premium_economy | success | 3 | 8 | 73.0 | — |
| Baseline | 4 | NRT–TPE | 2026-11-25 | economy | success | 3 | 9 | 93.1 | — |
| Baseline | 5 | TPE–SIN | 2026-11-25 | business | success | 2 | 5 | 80.0 | — |
| Baseline | 6 | JFK–TPE | 2026-11-24 | economy | success | 1 | 3 | 131.1 | — |
| Baseline | 7 | HND–SIN | 2026-11-26 | business | success | 4 | 6 | 59.0 | — |
| Baseline | 8 | TPE–HKG | 2026-11-25 | economy | success | 9 | 18 | 77.0 | — |
| Baseline | 9 | TPE–LAX | 2026-11-25 | business | success | 3 | 8 | 84.1 | — |
| Baseline | 10 | NRT–TPE | 2026-11-26 | premium_economy | success | 0 | 0 | 25.0 | — |
| Baseline | 11 | LHR–TPE | 2026-11-24 | economy | success | 2 | 6 | 140.1 | — |
| Baseline | 12 | TPE–SFO | 2026-11-25 | premium_economy | success | 3 | 7 | 93.1 | — |
| Baseline | 13 | HND–SIN | 2026-11-27 | economy | success | 0 | 0 | 29.0 | — |
| Baseline | 14 | TPE–HKG | 2026-11-26 | business | success | 7 | 19 | 75.0 | — |
| Baseline | 15 | TPE–LAX | 2026-11-26 | economy | success | 3 | 9 | 94.1 | — |
| Baseline | 16 | KIX–TPE | 2026-11-24 | economy | success | 3 | 6 | 75.0 | — |
| Baseline | 17 | TPE–SIN | 2026-11-26 | premium_economy | success | 2 | 6 | 149.1 | — |
| Baseline | 18 | JFK–TPE | 2026-11-25 | business | failed | 0 | 0 | 39.0 | search_protocol |
| Baseline | 19 | HND–SIN | 2026-11-28 | economy | success | 1 | 1 | 43.0 | — |
| Baseline | 20 | TPE–HKG | 2026-11-27 | economy | success | 9 | 17 | 72.0 | — |
| Baseline | 21 | TPE–LAX | 2026-11-27 | premium_economy | success | 3 | 3 | 42.0 | — |
| Baseline | 22 | NRT–TPE | 2026-11-27 | business | success | 3 | 5 | 56.0 | — |
| Baseline | 23 | LHR–TPE | 2026-11-25 | business | success | 2 | 5 | 119.1 | — |
| Baseline | 24 | SFO–TPE | 2026-11-26 | economy | success | 3 | 8 | 95.1 | — |
| Diagnostic repeats | 1 | JFK–TPE | 2026-11-25 | business | success | 1 | 1 | 40.1 | — |
| Diagnostic repeats | 2 | JFK–TPE | 2026-11-25 | business | success | 1 | 1 | 41.0 | — |
| Diagnostic repeats | 3 | JFK–TPE | 2026-11-24 | economy | success | 1 | 3 | 75.0 | — |
| Diagnostic repeats | 4 | LHR–TPE | 2026-11-25 | business | failed | 0 | 0 | 78.0 | price_changed |
| After fare-change hardening | 1 | JFK–TPE | 2026-11-26 | business | success | 1 | 3 | 89.1 | — |
| After fare-change hardening | 2 | JFK–TPE | 2026-11-27 | business | success | 1 | 3 | 107.1 | — |
| After fare-change hardening | 3 | CDG–TPE | 2026-11-25 | business | success | 1 | 2 | 68.0 | — |
| After fare-change hardening | 4 | HND–SIN | 2026-11-29 | economy | success | 1 | 1 | 49.0 | — |
| After fare-change hardening | 5 | TPE–HKG | 2026-11-28 | business | success | 6 | 12 | 74.0 | — |
| After fare-change hardening | 6 | TPE–LAX | 2026-11-28 | business | success | 2 | 2 | 50.0 | — |
| After fare-change hardening | 7 | TPE–SIN | 2026-11-28 | economy | success | 2 | 6 | 96.1 | — |
| After fare-change hardening | 8 | NRT–TPE | 2026-11-28 | economy | success | 3 | 8 | 137.1 | — |
| Final validation | 1 | LHR–TPE | 2026-11-25 | business | success | 1 | 3 | 98.1 | — |
| Final validation | 2 | JFK–TPE | 2026-11-25 | business | success | 1 | 1 | 41.0 | — |
| Final validation | 3 | TPE–HKG | 2026-11-24 | business | success | 7 | 21 | 89.1 | — |
| Final validation | 4 | HND–SIN | 2026-11-26 | business | success | 4 | 4 | 45.0 | — |

## Repeat the measurement

Start the app with go run . and create cases.json as an array, for example:

```json
[{"provider":"eva","origin":"TPE","destination":"HKG","date":"2026-11-24","cabin":"economy","adults":1}]
```

Then run:

```sh
go run ./cmd/eva-check -cases cases.json -out measurements.json
```

Update departure dates when repeating later. The command bypasses the result cache,
runs sequentially, saves each outcome, and prints summary counts. It makes real
airline/solver requests and stops at account, rate-limit or uncertain-outcome boundaries.
