# EVA member sessions

Read [login and OTP](code-walkthrough.md#login-and-otp) for the function-by-function
path. A member login and a flight booking context are separate objects.

## Snapshot and ownership

A portable member snapshot lives under .state/eva/<account hash>.json in SQLite's
states table. The path is a logical key, not a browser profile directory.
It contains scoped cookies, browser identity, creation/expiry information, the
configuration fingerprint and the exact sticky proxy URL/template.

The fleet permits one operation per account. The provider also acquires its gate
and OS-backed state lock. A saved session cannot safely drive concurrent booking
flows; different accounts provide concurrency.

## Reuse rules

1. Load the snapshot and verify format, configuration identity and timestamps.
2. Resume its original sticky proxy. If that exact sticky identity is busy, wait within the
   operation deadline; do not change IP underneath existing cookies.
3. Restore scoped cookies and verify the exit IP.
4. Verify the authenticated member page. An exclusively retained warm client may
   reuse a verification performed at most two minutes ago for a search; explicit
   preparation always verifies.
5. Save refreshed cookies only after successful verification/use.
6. A recognized expired login, removed proxy or recoverable proxy/verification
   rejection may invalidate the snapshot and perform bounded renewal.
   An uncertain credential submission is not immediately repeated.

The expiry is the earlier of the EVAID cookie deadline and the configured maximum
age (default three hours), minus five minutes. An already earlier saved expiry
is preserved. Server revocation and residential-peer loss may invalidate it sooner.

Warm transports are memory-only. They are discarded after a failed operation,
changed snapshot/proxy, more than five minutes idle, or shutdown. Persistent
snapshots survive restarts; open network connections do not.

## Background preparation

Every 30 seconds, when enabled and globally configured, the fleet checks accounts.
Missing/expired sessions go first, then the least recently checked accounts.
Healthy idle sessions become due after five minutes.

Background work uses at most max(1, min(8, floor(shared concurrency / 2))) slots.
The shared account/registration ceiling still applies. Bulk manual preparation
can use the shared ceiling and deduplicates queued/busy accounts.

Failed accounts retry after a stable per-account/attempt delay of 15–30 minutes.
A longer EVA Retry-After is honored. After 15 consecutive failed operations the
account pauses for attention. Success resets the count. The retry time/count are
persisted; restart does not shorten them. Older permanent pauses are upgraded to
a scheduled first retry. See [exact recovery rules](reliability.md).

## Portability

There is no machine-specific encryption. Stop the process before copying the
workspace database. The account configuration, original sticky proxy/IP and
upstream session must remain valid. See [storage](storage.md).
