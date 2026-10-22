# Design decisions and tradeoffs

Read alongside the [code walkthrough](code-walkthrough.md). These describe the
current implementation and practical alternatives, not a claim that every option
has been load-tested.

| Decision | Why / benefit | Cost or limit | Alternative and when it helps |
| --- | --- | --- | --- |
| Go backend with goroutines | One deployable process; cheap concurrent network waits; cancellation through context | Goroutines do not remove upstream bottlenecks; locks and ownership still matter | Separate worker processes when isolation or multiple hosts are needed |
| Embedded plain HTML/CSS/JS | go run . boots everything; no frontend toolchain/runtime needed | Larger hand-maintained scripts; source edits require restart/rebuild | A component framework/build pipeline when UI complexity merits it |
| Request-based airline integration | Explicit state transitions, reusable connections, lower local resource use than a browser per search | Must track markup/security changes and exact state dependencies | Browser automation for unsupported flows, with higher memory/CPU and operational complexity |
| Browser-like TLS transport | Keeps TLS/header/user-agent identity compatible across requests and solver inputs | Dependency/profile updates can break compatibility; impersonation is not proof of acceptance | Standard net/http where the upstream accepts it; revalidate before changing |
| SQLite with pure-Go driver and WAL | Cross-platform, no database service or C compiler; portable workspace | Writes serialize; JSON analytics/history grow; single local workspace | A server database and explicit schema/indexes for multiple writers/hosts or large analytics |
| Portable JSON snapshots in SQLite | Retains cookie scope/expiry, identity and exact proxy with a small logical record | Plaintext bearer material; corrupt/revoked state cannot be trusted | Encrypted storage/secret manager for a hosted service; browser profile storage is heavier |
| One account per live flow | Avoids cart/session mutation races and duplicate credential work | Account supply bounds live parallelism | Independent supported upstream sessions per account only if verified safe |
| Bounded FIFO and shared limit | Backpressure; separate capacity/execution budgets; live limit reductions drain safely | Finite waiting budget; no cross-host replay or fair tenant scheduler | Durable broker/worker service with admission control and visibility leases |
| Sticky identity per member session | Preserves egress identity; distinct saved IDs can overlap on one template | Residential peers disappear; fresh allocation still prefers unused templates | Distributed identity leases when multiple worker hosts are introduced |
| Warm HTTP client plus saved member state | Reuses TLS connections and sometimes recent verification | Still needs IP checks/fresh booking context; memory-only optimization | Pool transports only across independently safe identities; never share cookie state |
| Thirty-minute complete-result cache | Avoids repeated security/pricing work; cheap repeats | Fares can change during TTL; currently keyed per account and query | Shared normalized result cache with source/session-independent keys and explicit freshness policy |
| Coalesce identical running queries | Prevents duplicate airline/solver work | Callers share job cancellation; works in one process only | Distributed single-flight and per-caller subscriptions/cancellation |
| Sequential pricing inside a cart | PAGE_TICKET/session/recommendation state rotates | Each distinct fare group adds upstream latency | Independent booking carts per quote with a global cap, if proven correct and worth solver cost |
| Group quote deduplication | One quote can populate multiple matching itinerary fares | Depends on stable recommendation identity | Stronger composite identity if upstream identifiers change |
| One pricing work list and indexed quote recovery | Counting/execution share eligibility rules; recovered quotes avoid repeated scans | Matching must keep flight/fare/price boundaries exact | Reconsider keys if EVA changes recommendation semantics |
| Registration workers wait for notifications | Idle workers avoid one SQLite query per worker per second; imports/retries wake promptly | Temporary database errors can wait for the 15-second upkeep fallback | A durable broker when onboarding spans multiple hosts |
| Exact cash normalization | Prevents per-adult/party bugs and floating-point equality errors | Decimal parsing/validation is more involved | Currency minor units if all supported currencies/precisions are explicitly defined |
| Theoretical maximum mileage offset | Supports aggregation independent of test account balance | Estimate may not be selectable/bookable by that account | Account-specific slider quotes for a purchase-oriented product |
| Solver abstraction with narrow fallback | Swappable provider service; can recover generation outages | Paid latency and external availability; fallback can duplicate cost if too broad | A single solver if simpler and demonstrably reliable; retain measured selection criteria |
| Layered bounded retries | Recover reads, known state changes and delayed account failures without replay storms | Some searches legitimately fail when outcome is uncertain | Blind retries appear simpler but can duplicate state-changing actions |
| Fifteen account failures before attention | Transient IP/mailbox/security failures do not permanently remove capacity | Persistent bad credentials may consume credits for hours before pausing | Lower configurable threshold/circuit breaker for a public service; current requested policy favors unattended recovery |
| Stable 15–30 minute retry spread | Restarts do not collapse retry timers; fleet retries are staggered | Recovery waits even when a transient outage clears quickly | Adaptive backoff based on error class and observed provider recovery |
| Background limit: half of slots, cap eight | Restores expired sessions while reserving capacity for searches | Very large fleets can fall behind upkeep; no separate renewal service | Deadline-aware preparation queue and configurable reserved capacity |
| Checkpointed registration | Restarts/page refreshes do not lose submitted membership state | Ambiguous submissions cannot always be automatically resolved | Provider idempotency/status APIs would allow stronger automatic recovery |
| Revision-aware polling and cloned previews | Simple reconnect; unchanged polls avoid payload copies/transfers | Changed previews still send a full snapshot | SSE/WebSocket updates with compact diffs |
| One coalescing history writer | SQL/encoding do not block scheduler status/cancellation; accepted work flushes before dispatch | Single writer; brief progress lag; hard failure can lose latest update | Transactional durable queue/outbox for distributed workers |
| Permanent history | Auditability, trends and reproducible failure analysis | Database and backups grow forever | Partition/archive older history while preserving access, rather than silently deleting it |
| Localhost-only admin | Fits the personal/local deployment; minimal authentication surface | Not a public multi-user product, no tenant isolation | Hosted identity, TLS, authorization, rate limits, audit and secret isolation |
| Shared provider contract | Separates scheduler/UI from EVA's implementation and allows fake tests | A small abstraction in a single-provider app | Direct coupling saves a little code but makes lifecycle tests and future refactors harder |

## Important invariants

1. Never use the same account/cart concurrently.
2. Never move an existing member snapshot to a different proxy IP silently.
3. Never cache an error or incomplete result as a complete success.
4. Never infer authentication from HTTP 200 alone.
5. Never interpret an error/security page as zero flights.
6. Never replay a credential, OTP, registration or checkout mutation merely because
   its response was lost.
7. Preserve upstream cookie scopes, changing form state and response identity.
8. Keep raw secrets and HTTP/email bodies out of public results and timelines.
9. Keep network waits outside database transactions.
10. Preserve history when clearing temporary cache or removing obsolete configuration.

## Why pricing is still slow

The dynamic catalogue call had a median of about 8.3 seconds in the recorded
benchmark. A unique quote requires fare selection, travellers, catalogue and
checkout preparation, plus availability refresh between groups. These are real
upstream requests with dependent state. Local concurrency helps across accounts;
adding goroutines inside the same mutable booking cart would be incorrect.

Avoiding the catalogue/checkout chain requires a verified alternate mileage
calculation endpoint, not just deleting calls that appear expensive. Until then,
deduplication, complete-result reuse and preauthenticated sessions are the safe
speed improvements already used.
