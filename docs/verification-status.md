# Verification scope

## Hosted demo — 25 October 2026

The Docker image built successfully and was deployed to one Fly.io machine in
Amsterdam with one shared CPU, 1 GB RAM and a 1 GB persistent volume. The private
SQLite transfer preserved 59 accounts and 5,000 proxy entries. Public HTTPS,
demo authentication, separate admin login, CSRF protection and API-key redaction
were checked against the hosted service; its health check passed.

Two uncached TPE–HKG smoke searches were submitted for 27 November 2026, economy,
one adult. The first failed during fresh EVA member login (`login_unverified`).
After background preparation established usable sessions, the second completed
in approximately 110 seconds with five award mileage quotes and five cash-derived
tax estimates. This is deployment smoke evidence, not a new reliability benchmark.
Normal background upkeep was restored after verification.

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
No actual award-tax quotation was verified on an unfunded account. These counts
describe the original benchmark; the targeted follow-up below used a later build.

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

## Tax-gap follow-up — 24 October 2026

Investigation identified an additional `TRANSBLOCK` handoff and booking-confirmation
CAPTCHA on the ICN cash-estimate route. The implementation now follows the exact
fresh form, uses route-specific high-score verification and handles the image
challenge when required. Explicit incorrect-code responses permit at most three
fresh confirmation submissions; transport failures do not replay a submitted form.
An optional independent ready account can retry missing fee enrichment within the
existing shared two-minute budget, without repeating award pricing or logging in.

The 11 original gap queries were replayed uncached at concurrency four. All 11
completed with award results in 4 minutes 25 seconds. Fourteen of the 17 previously
missing estimates were returned with USD conversion. Three remained unknown across
two TPE–ICN searches on 27 November 2026: economy for six adults and business for
one adult. Both exhausted explicit CAPTCHA-rejection recovery, including the
independent account. The SIN–TPE six-adult premium-economy gap recovered both fees.
This is a targeted regression run, not another 200-search benchmark or a claim
that CAPTCHA acceptance is guaranteed.

Automatic account maintenance now pauses after one hour without a submitted
search. Cached searches also count as activity; administrative page polling does
not. Activity survives restart, the next search resumes maintenance, and manual
preparation remains available while idle.

The complete offline Go suite, frontend regression suite, `go vet` and Windows
build passed for this follow-up build.

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
