# Verification scope — October 20, 2026

## ANA

Latest reliability checks: five proxied checks failed (the last four confirmed
HTTP 403 at credentials); a direct control returned MFA required and testing
stopped. No proxy success or production-scale throughput is established.
The editable 100-entry pool, locked leases, IP verification and bounded retry
behavior are implemented. A synthetic 1,000-concurrent-start test verifies
that the service starts only one checker. See [details](reliability.md).

Earlier performance check: after removing fixed request spacing and skipping
ANA's optional pixel step, a fresh login verified the matching member in
3.95 seconds (excluding compilation). It used two SBSD and three sensor
generations, with one IP lookup per session. Offline tests, `go vet`, and
the application build passed after these changes. BA was not live-tested.

One fresh direct login was verified using Hyper Solutions for Akamai. The run
completed pixel/SBSD initialization, three sensor rounds, authorization, SSO,
and an authenticated member check matching the requested temporary account.
No captured cookies/tokens were replayed and no flight search was performed.
The session was closed after verification.

The frontend uses that same login module through a background-check service.
Offline tests cover start, status, cancellation, duplicate submission, cooldown,
error sanitization, and CSRF protection with BA search disabled. The real local
page was visually checked in its unconfigured state. The frontend addition did
not trigger another paid live login or award search.

Not verified: other accounts, proxy login, MFA, long-lived session reuse, or
repeatability across upstream changes. ANA award search is not implemented.

## British Airways

The pre-existing integration remains experimental. Historical evidence records
one successful JFK–LAX economy diagnostic for one adult on October 29, 2026,
with 12 itineraries. That diagnostic used a Node helper alongside Go; it does
not verify repeatable login through the standalone production provider.
Later diagnostic logins failed. The cause remains unconfirmed.

This cleanup did not run a new BA login or search. Historical captures and
research scripts remain in the original project location, outside this repo.

## Local validation

The full offline Go test suite, `go vet`, JavaScript syntax checks, backend build,
and PowerShell launcher parsing passed. Tests do not contact airlines or incur
solver charges. No credentials or solver keys are included in the repository.
