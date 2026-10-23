# Verification scope

## Final award benchmark — 23–24 October 2026

The final reviewed build completed **200 distinct uncached award searches**, with
no manual resubmissions and no final failures. Before the run, search history and
temporary result caches were backed up and reset; accounts, proxies, settings and
saved sessions were retained. The test used eight routes, four departure dates
(24/27 November, 10 December 2026 and 15 January 2027), three cabins and one/two/
four/six adults, shuffled with seed 20261023. Every query used `refresh: true`;
all 200 job IDs were distinct and none returned a cached result.

| Submitted concurrency | Searches | Priced / valid empty | Recovered successes | Final failures | Phase elapsed |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 2 | 20 | 10 / 10 | 6 | 0 | 506.5 s |
| 8 | 40 | 18 / 22 | 8 | 0 | 248.3 s |
| 20 | 80 | 31 / 49 | 11 | 0 | 190.6 s |
| 30 | 60 | 20 / 40 | 7 | 0 | 170.5 s |
| Total | 200 | 79 / 121 | 32 | 0 | 18 min 36 s |

There were 168 successes without recorded recovery (84%) and 32 after automatic
recovery (16%): 23 needed award/session recovery; nine needed recovery only in
optional fee enrichment. These are job outcomes, not counts of first-attempt HTTP
successes. The 53 recorded recovery events comprised 15 connection interruptions,
12 challenge rejections, ten cash-search protocol errors, seven award protocol
errors, five session denials, three generic provider errors and one explicit
incorrect CAPTCHA. One job can record several events. Seven jobs reused exact
recent mileage quotes after fresh availability checks. No failure was reclassified
as empty inventory to obtain the success count.

| Completed result cohort | Count | Median | P95 | Maximum |
| --- | ---: | ---: | ---: | ---: |
| All | 200 | 18.7 s | 118.0 s | 170.5 s |
| With mileage quotes | 79 | 60.5 s | 150.2 s | 170.5 s |
| Valid empty availability | 121 | 12.1 s | 39.4 s | 149.3 s |

Among priced searches, the first mileage appeared at a median of 27.3 seconds
(P95 45.4 seconds). Optional fee enrichment took a median 15.3 seconds (P95 36.2
seconds), including setup/recovery/FX rather than just one tax HTTP request.
Latencies include submission, waiting and automatic recovery, measured by a
one-second polling client. Medians average the central pair for even cohorts;
P95 uses nearest rank.

The run returned 242 exact party-mileage quotes and 225 separately labelled
cash-derived tax estimates, all converted to USD (93.0% quote coverage). All 225
used the validated bulk cash-availability path. Ten TPE–ICN searches lacked
15 estimates because the cash form response did not expose the expected booking
handoff; all their award quotes succeeded. EVA's form uses `ICN`, so changing it
to city code `SEL` is not a supported fix. One six-adult SIN–TPE search lacked
two estimates after cash-handoff/proxy interruptions; optional enrichment refused
to start a fresh credential login. These 17 fees remain unknown, not zero.
No actual award-tax quotation was verified on an unfunded account.

An independent result audit checked query/itinerary endpoints and dates, segment
fields/cabin values, positive integer party-mileage totals, checked party sizes and USD
arithmetic across all 242 quotes and 248 segments, with no issues found.

Logs recorded 46 parallel quote batches and 46 completed joins. Helpers supplied
77 accepted quotes from 98 assigned; eight helper failures and eight parent-driven
cancellations left missing work for ordinary parent recovery. There were 67 fresh
parent-inventory proof uses, aged 3.7–34.0 seconds. These normally avoid duplicate
three-round availability preparation; fallback can add that work back, so they
are not a measured invoice saving or a guarantee of three fewer calls every time.

The server used `GOMAXPROCS=4`, a 512 MiB soft Go memory limit and a shared limit
of 30 account operations, including optional helpers and maintenance. Its Windows
peak working set was 301.6 MiB. The final source was unchanged throughout the run.
An earlier run was deliberately stopped before quote parallelism/bulk-tax changes;
its results and exploratory smoke checks are excluded from these 200 outcomes.

Phases contain different queries and increasingly warm accounts. They are not a
controlled comparison proving that higher concurrency lowers latency. The 121
empty results also make the overall median unsuitable for estimating priced-search
speed. Zero failures in this sample is not a zero future failure guarantee or a
multi-day session-renewal soak test. Sanitized counts and cohort details are in
[the machine-readable summary](benchmarks/award-200-2026-10-24.json); raw responses,
job identifiers and private workspace diagnostics are not published.

## Reviewed build and offline checks — 24 October 2026

The complete Go suite, frontend Node regression suite, `go vet` and Windows build
passed before the final live benchmark. A `CGO_ENABLED=0` macOS ARM64 cross-build
also passed; this is compilation evidence, not a live macOS airline test. The Go
race detector was not run because this host has no C compiler and CGO is disabled.

The senior review resulted in regression-covered fixes for restoring more than
1,000 unfinished jobs, interrupted-job UI handling, detaching a viewer without
cancelling shared work, bounded Outlook transient-error polling and shared FX
outage backoff. Separate tests cover independent-account quote helpers, global
capacity, ownership release, panic/cancellation cleanup, fresh party-evidence
matching and expiry, bounded setup fallback and preserved helper recovery events.

Bulk cash-tax tests reconcile flight references, fare families, currencies,
per-adult versus whole-party units and base/tax/total arithmetic. Four live
availability-versus-selected-FARE comparisons agreed: TPE–HKG for four adults
(TWD 12,188) and HKG–TPE for two adults (HKD 1,388), two flights on each route.
These validate the cash estimate source, not actual award-ticket charges.

Ordinary Go/JavaScript/CSS/HTML/PowerShell source comments were removed with
syntax-aware checks. Five required Go build/embed directives remain. Test fixtures,
documentation, dependency metadata and licenses are not treated as source comments.

## Earlier award development trials — 23 October 2026

Two uncached mixed-load batches each submitted 120 award searches, up to twelve
at once, across SIN–TPE, TPE–SIN, TPE–HKG, HKG–TPE, HND–SIN, TPE–NRT, TPE–ICN
and TPE–LAX. They covered economy, premium economy and business, one/two/four/six
adults, and departures on 24 November, 10 December and 15 January.

| Batch | No recorded recovery | Completed after recovery | Final failures | User success |
| --- | ---: | ---: | ---: | ---: |
| First mixed run | 105 | 11 | 4 | 116/120 (96.7%) |
| After connection/captcha/airport-transfer recovery fixes | 110 | 9 | 1 | 119/120 (99.2%) |

The second run returned 49 searches with mileage quotes (164 itineraries) and 70
valid empty availability results. Empty inventory is a complete search, not a
priced result. Its recorded recovery events included connection interruption,
CAPTCHA rejection, security/session rejection and changed response state; a job
can record more than one recovery. Two jobs reused already-finished quotes after
fresh inventory/itinerary validation.

Successful-job latency includes queueing, account preparation and recovery:

| Result group | Median | 95th percentile | Maximum |
| --- | ---: | ---: | ---: |
| All 119 completed searches | 17.2 s | 147.5 s | 350.0 s |
| 49 searches with mileage quotes | 51.5 s | 165.5 s | 350.0 s |
| 70 valid empty results | 13.1 s | 47.5 s | 157.6 s |

Among priced searches, median time to the first quote was 25.2 seconds. These are
this machine's observed results, not production guarantees or an upstream SLA.

The one final failure was an adaptive-security timeout before the intercepted
login request was forwarded. The award-only policy now permits another account
at that boundary; uncertain credential/OTP forwarding is still excluded. All
five failing query cases from the two batches subsequently completed in targeted
live checks. That particular 120-case batch was not repeated after the last policy
fix; the later mixed 200-search validation uses a different query sample.

A separate TPE–HKG search for 27 November exposed a blank availability placeholder
beside five positively available flights. Repeated account recovery could not fix
that deterministic parser gap. It now excludes only that blank row, with a
regression preserving its available siblings. The status audit and diagnostic
rules are documented in [award flow](eva-awards.md#status-interpretation).

After that parser fix, ten uncached TPE–HKG checks all completed, up to six at
once, across 26/27/28 November and 1 December, one/four adults, and three cabins.
Six completed without a recorded recovery and four recovered automatically.
Nine returned 51 priced itineraries in total; one was valid empty inventory.
The exact previously failed 27 November, one-adult economy query returned five
flights at 10,000 miles each. The four-adult query returned four flights at 40,000
miles each. No manual retries were used. Three jobs reused exact completed quotes
after fresh availability. Median completion was 128.2 seconds; the slowest took
610.2 seconds, so a zero-failure sample does not imply consistently low latency.

Offline regressions additionally cover fresh state per quote, per-person/party
mileage, US/GB dates, padded flight numbers, actual mixed cabins, airport changes,
five-account recovery bounds, cancellation, partial preservation, exact quote
reuse/expiry and its original timestamp across repeated recovery, and separation
of authentication uncertainty from quotation continuation. Frontend regressions
cover mode/party limits, unknown taxes, partial-result messaging and date-picker
restoration. No payment/ticketing call is made by the award flow.

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
