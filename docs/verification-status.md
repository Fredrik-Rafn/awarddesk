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
