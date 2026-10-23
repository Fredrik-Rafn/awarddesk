# Verification scope

## Recorded live EVA checks — 22 October 2026

The 500-search uncached baseline used up to 30 simultaneous searches: 423 completed
(402 with priced flights, 21 valid empty results), and 77 failed. Fourteen completed
jobs recovered from a recorded failure.

Thirty-two failures were false price changes caused by comparing per-adult
availability amounts with party checkout totals. After fixing that comparison,
seven targeted live searches completed with all 102 fare entries quoted, including
two- and three-adult cases. One genuine availability change recovered normally.

The full 500-search baseline has not been repeated after the fix. TPE–ICN handoffs
and some missing booking-data responses remain unresolved. Measurements describe
those runs and are not a guarantee of future success.

## EVA-only cleanup and maintenance changes

Regression checks cover the EVA-only API/configuration, retained security adapters,
account ownership, retry delays, persisted retry budget, old-status upgrade,
provider cooldowns and preservation of EVA state/history during cleanup.
Offline tests do not contact EVA or consume solver credits.

A fresh-workspace HTTP smoke check verifies startup, embedded pages, configuration,
admin authentication and unconfigured-login behavior. This is not a new live airline
benchmark. See [capacity](capacity.md) for measured facts versus estimates.

## Queue and contention regressions

Tests cover separate queue/execution budgets, first-account timing, 1,024 queued
admissions, cancellation freeing space, cache/deduplication at saturation, lower
concurrency draining active work, blocked SQL/cache leaving status/cancellation
responsive, immutable snapshots and ETag/304 polling in Go and JavaScript. Proxy
tests cover independent saved IDs sharing a template, same-ID exclusion, isolated
quarantine, release wakeups and immediate invalidation after Admin edits. A real
SQLite/fake-provider load test covers 300 jobs at peak 100 workers: 270 completed,
30 cancelled, all terminal records retained. This is not a live EVA benchmark.

After deploying these changes, two simultaneous uncached economy searches passed:
HKG–TPE, 10 December 2026, two adults (115 seconds, seven flights), and TPE–HKG,
24 November 2026, one adult (175 seconds, seven flights). Both returned complete
results. Their unchanged polls returned 115 empty 304 responses in total. A repeat
of the latter query returned complete cached results in 6 ms on localhost.
These are two smoke checks, not a replacement mixed-load benchmark or an SLA.

## Code review cleanup

Additional regressions cover notification wakeups for imports/retries/restarts,
single ownership of a queued signup, lowered onboarding limits, deduplicated fare
plans, missing cash prices stopping before upstream calls, and quote reuse rejecting
changed flights, departure times, connections, families, cabins, currencies and
prices. Preserved quotes retain fresh IDs and do not share mutable mileage data.
The full Go suite and vet are rerun after these changes; offline checks use no
airline requests or solver credits.

The reviewed build also passed an uncached HKG–TPE search for 10 December 2026,
two adults: 131 seconds, seven flights, all 21 displayed fares quoted, complete
results and no recorded recoveries. This single smoke check validates the changed
pricing path; it does not establish a new latency or failure-rate benchmark.

## Test layout and module boundaries — 23 October 2026

All 196 Go test functions were retained during the layout cleanup. HTTP/admin and
job/SQLite scenarios now run from `tests/integration/` through exported interfaces;
frontend JavaScript tests run from `tests/browser/` with Node. Private unit tests
remain with their Go packages. The HTTP tests obtain CSRF tokens via `/api/config`
rather than accessing the server's private field.

EVA provider setup, navigation and saved member state were split out of the login
file. Admin account/settings/overview handlers were split from common routing and
authentication. Verification uses the offline suite, vet and compilation; no live
EVA flow or paid solver call is part of this cleanup. See
[tests/README.md](../tests/README.md) for exact commands and scope.

## ngrok demo mode — 23 October 2026

Offline integration tests cover exact public-host matching, required demo
authentication, HTTPS/origin/CSRF enforcement, local access preservation, blocked
public admin/preparation endpoints (including requests with a valid admin cookie),
and simulated search submission/result polling. Invalid startup URLs and short
passwords fail closed. The Windows launcher is syntax-checked, and its flags are
checked against the installed ngrok 3.36.0 help. No public tunnel or live EVA flow
is started for these checks; the actual assigned ngrok URL is obtained when the
user runs the launcher.
