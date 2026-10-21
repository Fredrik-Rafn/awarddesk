# Login reliability and proxy operation

Current configuration and proxy lists are managed in Admin and stored in SQLite.
Existing `.env`, `proxies.txt` and JSON session files are imported once. See
[storage and migration](storage.md). The historical measurements below describe
their recorded test runs; they are not a guarantee of zero provider failures.

The pool currently supports authenticated Evomi endpoints. A new lease replaces
the exported session ID with a fresh random ten-character `lockedsession` ID
and removes `lifetime`, leaving the configured template unchanged. All airline requests
in an attempt use that URL and one isolated cookie jar. An exit-IP lookup during
initialization is compared with another immediately before credentials.

Evomi documents that ordinary `session` can replace its IP for stability,
whereas `lockedsession` fails when its bound peer goes offline and does not
support `lifetime`. The 150-second application deadline bounds the full login,
including its one possible fresh-session retry.
Source: https://docs.evomi.com/proxy-instructions/residential-proxies/proxy-sessions/

## Retries and concurrent traffic

Explicit ANA OTP/MFA responses now show `otp_required`, without permanently
blocking the account. Old `.state/ana/*.blocked` markers are no longer read or
written. A one-minute cooldown still applies before a manually requested new
login. The CLI stops its batch at OTP. Automatic Outlook OTP handling is not
implemented; the current attempt closes its session when OTP is requested.
Timeouts alone are not classified as OTP.

- No fixed spacing between requests. Dependent steps remain sequential.
- At most one fresh-session retry for transport/read/HTTP 5xx failures before
  credentials. A failed transport lease cools down for 60 seconds.
- Credential POSTs are never replayed automatically. A connection failure
  after submission is reported as an uncertain outcome.
- HTTP 403 is reported as access denied, distinct from a broken connection.
- Pool leases are exclusive and acquisition does not create a waiting queue.
- The UI service coalesces concurrent starts into one check. The provider also
  prevents concurrent logins for its configured account.
- Successful UI check results are reused for 15 seconds with the original finish
  timestamp. Authenticated state is retained separately in a JSON file;
  the manager validates it before use.
- Failed checks cool down for five seconds, account failures for one minute;
  longer upstream retry intervals are respected.

This is a bounded single-process app with one configured ANA account, not a
distributed high-throughput account service. A 1,000-concurrent-start synthetic
test verifies one checker invocation; it does not measure ANA throughput.
Multiple server replicas would need shared account locks, admission control
and shared session ownership. The existing UI/API remains localhost-only.
The proxy pool currently applies to ANA; BA retains `AWARDDESK_PROXY`.

The server now runs an automatic saved-session job. See [sessions](sessions.md).
The standalone diagnostic CLI continues to perform fresh login checks.

## Live findings on October 20, 2026

Five proxied login checks failed: the first returned an authentication-category
error before HTTP status diagnostics were added; the next three returned
HTTP 403 at credentials in 16.42, 53.85 and 34.18 seconds. The 53.85-second run
recovered from one pre-credential connection failure with a fresh session.
All three pre-credential IP comparisons passed.

A fourth status-instrumented proxy check restored pixel initialization and
still returned HTTP 403 at credentials, in 6.61 seconds, with a matching IP.
Pixel was therefore returned to its previous disabled setting for ANA.
Successful security submissions alone do not establish accepted authentication.

A final direct control check reached an explicit MFA requirement in 3.84
seconds. Live testing stopped. The direct control temporarily bypassed pool
construction; its CLI metadata still reports the configured pool environment.
This test does not establish that the proxy rejection is exclusively IP-related.
No proxy login succeeded in these checks. Earlier direct success at 3.95 seconds
remains historical evidence, not a current reliability guarantee.

After the account holder completes ANA's browser verification, an explicitly
requested live check can be run with:

```powershell
go run ./cmd/ana-login -runs 3 -report results.json
```

The CLI performs fresh sequential checks, stops on MFA/account/rate-limit
responses, and saves sanitized timing/error metadata. `-runs` is bounded to ten.
No award searches were implemented or performed.

## SQLite and multi-account verification (21 October 2026)

The local workspace now uses SQLite and bounded account workers. Automated tests
cover 18 searches across three simulated accounts with no per-account overlap,
ready-session preference, safe account failover, uncertain-outcome exclusions,
manual/background preparation coalescing, account edits while busy, cancellation,
external account leases, concurrent SQLite writes, one-time migration, admin
CSRF/authentication/secret masking, and persisted search results after restart.

Live checks during this change:

| Check | Result |
| --- | --- |
| First automatic login preparation | EVA HTTP 403; status recorded and backed off |
| Next automatic preparation | Successful, session saved in SQLite |
| TPE–HKG first search | Six flights found; selected-fare pricing context missing after a 41-second response; job failed with partial availability |
| HND–SIN business, 30 November 2026 | Complete, four flights, 39.1 seconds |
| TPE–HKG retest, same date/cabin | Complete, six flights, 75.2 seconds |
| Cached HND–SIN | Complete in 4 ms; 1,800-second cache TTL; repeated requests shared a cached job |
| Cached TPE–HKG after restart | Complete in 8 ms; six flights; 1,800-second cache TTL |
| Concurrent authenticated admin overview requests | 24/24 successful; maximum 90 ms |

The missing selected-fare-state case now has a bounded availability refresh before
checkout, with regression coverage. It never infers a price or repeats a completed
checkout. The live retest succeeded without needing that recovery, so its success
must not be presented as live proof that the new recovery executed. These checks
are functional evidence, not a meaningful new provider failure-rate estimate.
Multiple real accounts are still needed for a live multi-account load test.
