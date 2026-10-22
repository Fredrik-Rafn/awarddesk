# Capacity and scaling

These are workload-specific measurements and planning estimates. The configured
ceiling is not a measured capacity guarantee. No additional 500-search test was
run as part of the EVA-only cleanup.

## What was measured

The 22 October 2026 test submitted 500 distinct uncached searches in phases up to
30 concurrent jobs, across multiple routes/dates/cabins/adult counts.

| Observation | Result |
| --- | --- |
| Total jobs / max concurrency | 500 / 30 |
| Elapsed test time | Approximately 43 minutes |
| Aggregate completion throughput | Approximately 11.6 jobs/minute, including failures |
| Completed jobs | 423 (84.6%); 402 priced and 21 valid empty |
| Failed jobs | 77 (15.4%) |
| Completed with recorded recovery | 14, included in 423 |
| Successful-job latency | Median 73.6 s; P95 193.9 s |
| Peak process resident memory | About 260 MiB |
| Searches with proxy queue wait | 83; median wait about 66 s |
| Distinct fare groups | Route-dependent; some long searches took several minutes |

The host had 12 logical CPUs and about 31.7 GiB RAM; the test process used
GOMAXPROCS=4 and a 1 GiB soft Go memory limit. Low observed memory does not prove a
200-worker capacity or an upstream success guarantee.

Thirty-two baseline failures were the per-adult/party price-comparison bug, now
fixed. Seven targeted live regression searches subsequently passed, including
multiple adults. The original 84.6% is the measured baseline, not the current
post-fix success rate. A full post-fix mixed-load run is still needed. Account
retry-policy changes also have not had a multi-hour soak test.

## Practical operating estimate today

Keep 30 as the tested live-concurrency ceiling for now. With enough ready accounts,
independent usable proxy templates and funded/healthy solvers, planning around
10–15 new uncached searches per minute is a conservative starting range based on
the mixed benchmark, not an SLA. Long-haul searches with many quote groups can
reduce that; cache hits and identical-query coalescing can increase user-facing
throughput substantially.

Do not call this 30 simultaneous users. One user can submit many jobs, and many
users can reuse cached/coalesced work. The public app is localhost-only; serving
real external users would need a separate deployment/security design.

The 1–512 setting is a software bound, not evidence that 512 live flows work.
Higher concurrency needs an incremental representative test and resource/error
measurements at each level.

## How to estimate throughput

Use mean occupied-slot time, not median latency:

```text
live concurrency = min(shared limit minus other work,
                       available account owners,
                       independent usable proxy leases,
                       upstream/solver capacity)

maximum steady arrival rate ≈ live concurrency / mean occupied-slot time
```

Illustrative saturation estimates, without queue/maintenance/solver bottlenecks:

| Live slots | Assumed mean 75 s | Assumed mean 120 s | Assumed mean 180 s |
| --- | ---: | ---: | ---: |
| 10 | 8/min | 5/min | 3.3/min |
| 20 | 16/min | 10/min | 6.7/min |
| 30 | 24/min | 15/min | 10/min |

These are arithmetic scenarios, not additional measurements. Operating at
saturation leaves no room for variance or account recovery. Queue latency grows
quickly when arrivals exceed completions. A 15-minute job deadline also limits
how large a burst can wait: the channel size is not a promise those jobs finish.

At 30 active browsers polling every five seconds, job polling alone is roughly
6 requests/second. That is modest HTTP volume, but responses can carry full flight
previews, and admin analytics/cache lookups have additional cost. Cached-request
RPS, hundreds of browser viewers and long-term large-history performance have not
been benchmarked.

## Constraints even with enough accounts

- Upstream latency and dependent per-fare pricing dominate slot time.
- The shared ceiling includes searches, preparation and onboarding.
- Background preparation can occupy up to eight slots at a 30-slot setting.
- Template-based proxy locking can serialize otherwise distinct sticky sessions.
- External solver quotas, funding and provider outages remain hard dependencies.
- SQL/JSON analytics, cache scans and repeated progress persistence can become
  local contention points; the SQLite pool currently has four connections.
- A large fleet needs ongoing renewal. If a login takes 100 s and effective
  lifetime is 175 minutes, 1,000 accounts require about 9.5 continuously occupied
  renewal slots just to keep up, before periodic verification. That illustrative
  requirement already exceeds the current eight-slot maintenance cap.
- Tick-based preparation batches and oldest-first ordering improve fairness but
  do not promise every account in an arbitrarily large fleet gets checked every
  five minutes.

## Scaling roadmap

### First: measure and improve one process

1. Repeat the mixed live benchmark after the pricing and recovery fixes. Separate
   fresh/warm, live/cached, route/fare-group count and passenger-count cohorts.
2. Record arrival rate, p50/p95/p99 queue time, account/lease wait, upstream stage
   times, solver latency/cost, final outcomes and recovered outcomes.
3. Resolve proxy-template contention through durable per-session allocation or
   actual sticky-identity leases, preserving same-IP ownership and quarantine.
4. Add bounded admission/backpressure based on queue age and expected work,
   rather than accepting a burst that cannot finish before its deadline.
5. Prioritize preparation by expiry deadline; spread renewals and reserve explicit
   login/search capacity. Add error-class circuit breakers and monitor retry spend.
6. Move fleet/cache metadata lookup out of broad scheduler locks and index a
   normalized shared-result cache. Throttle/coalesce progress writes.
7. Use incremental progress delivery and cached/precomputed admin analytics.
   Keep permanent history queryable without scanning every full JSON job.
8. Increase live concurrency in measured steps, with rollback thresholds for
   latency, terminal error rate, solver saturation and proxy churn.

### Then: multiple workers/hosts

- Separate API/UI from a durable search worker queue.
- Use a shared server database for jobs, accounts, normalized analytics and
  expiring distributed ownership leases. SQLite file locks are not a multi-host
  coordination mechanism; do not put the same WAL database on a network share.
- Assign each account/session to one worker owner, with heartbeats, fencing and
  recovery for uncertain in-flight operations. A process crash must not create
  concurrent owners or automatically replay a checkout.
- Store encrypted secrets with restricted worker access. Centralize shared cache,
  deduplication, rate budgets and proxy assignment.
- Partition history while keeping all-time access, and maintain tested backups.
- Add public authentication/authorization, TLS, tenant isolation, quotas and
  observability before exposing the service externally.

External-account and solver dependencies still limit scale after local
infrastructure is distributed. More servers cannot remove sequential work inside
one booking context or guarantee airline acceptance.
