# Recovery and account readiness

Source: [fleet.go](../internal/fleet/fleet.go), [recovery.go](../internal/fleet/recovery.go),
[prepare.go](../internal/fleet/prepare.go), [login.go](../internal/providers/eva/login.go),
[session_verification.go](../internal/providers/eva/session_verification.go).

## Account retry rules

| Event | Rule |
| --- | --- |
| Successful search/preparation | Clear consecutive failures and next retry |
| Failed account operation | Increment durable consecutive-failure count |
| Failures 1–14 | Schedule a fresh check 15–30 minutes later |
| Longer provider Retry-After | Honor the longer pause |
| Fifteenth consecutive failure | Needs attention; no automatic attempt 16 |
| Explicit Prepare after exhaustion | Re-arm the account's attempt budget |
| Cancellation | Preserve prior retry budget; do not quarantine a healthy account |
| Mileage-pricing error | Keep the account available; do not treat that quote failure as broken login |
| Disabled/incomplete account | Do not invent credentials or automatically enable it |
| Restart with old permanent pause | Convert to first scheduled retry |
| Restart during an operation | Record interruption and delay a fresh check; no request replay |

The delay uses a SHA-256-derived stable spread based on account ID and failure
number. It is not a fixed sleep holding a worker: NextAttempt lives in SQLite
and maintenance selects due accounts on subsequent ticks.

The counter is an account-operation failure count, not an exact count of HTTP
credential submissions. A failure can happen earlier at a proxy, CAPTCHA or
mailbox step. Manual retry/continue of an unfinished registration follows its
separate durable checkpoint rules; see [registration](registration.md).

## Proxy and IP rules

- A fresh login acquires the next available, non-quarantined pool template in
  round-robin order and generates a new random lockedsession ID.
- That exact proxy is retained for the whole flow and saved with successful login.
- A resumed session uses the original proxy identity. IP pinning detects a changed
  exit; old cookies are never silently moved to another IP.
- Failed transport/preflight or explicit HTTP 403 session rejection can quarantine
  the proxy lease. The pool's failed-lease cooldown is one minute.
- Before credentials, recoverable setup failures allow one bounded renewal on a
  fresh proxy. Explicit wrong image CAPTCHA allows one fresh verification attempt.
- After credential/OTP transport uncertainty, the current attempt ends. A later
  scheduled account check is a new bounded operation. Failed fresh login has no
  successful snapshot, so it acquires a fresh sticky identity on that later check.
- More proxy strings do not guarantee different exit IPs or healthy peers.

A known capacity limitation remains: leases are exclusive by template, so different
saved sticky URLs derived from the same template can contend. More accounts do
not automatically remove that bottleneck.

## Search retry boundaries

The transport retries GET once on transient connection/read/server failure, with
a two-second pause. It does not generically retry POST. Explicit provider-level
exceptions operate only on recognized safe state transitions.

A search can fail over across at most three accounts before meaningful pricing.
OutcomeUnknown, credential/OTP stages and partial itineraries prevent blind
whole-search failover. Pricing may refresh availability and replan at most twice,
preserving quotes whose identity/price still match. A real total-price mismatch
still stops; the corrected per-adult normalization does not disable that check.

The frontend refreshes its request token and retries once only on an explicit
pre-handler request-token rejection. A lost mutation response is not replayed.

## Meaning of the UI states

Ready requires a locally valid snapshot and no blocking cooldown/attention flag.
Busy/queued reflects ownership and scheduled preparation. Retry scheduled means
automatic recovery is pending and shows its time plus consecutive-failure count.
Needs attention means the preparation retry budget is exhausted, or a separate
onboarding checkpoint requires inspection. Not ready means no usable session and
no active scheduled cooldown; it should be picked up by enabled maintenance when
capacity/configuration allow.

Failures are not erased when a later search succeeds. Job history distinguishes
completed jobs with recoveries, terminal failures, cancellation and interruption.
