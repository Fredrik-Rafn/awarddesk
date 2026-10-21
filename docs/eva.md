# EVA Air — Miles & Cash

Run the entire application from the repository directory with `go run .`.
The backend, session jobs and embedded frontend share port 8080. Restart the
process after editing Go or frontend files. Configure accounts and keys in Admin.

`EVA_IMPERVA_SOLVER=auto` (the default) uses Hyper for Incapsula/Reese84 and
tries AnySolver if Hyper's generation service fails. `hyper` and `anysolver`
force one provider for diagnostics. AnySolver uses its Salamoonder Reese84 sensor
task. Both submit the generated sensor through the session's own sticky proxy.
Provider connection errors, cancellations, unknown challenge formats and
uncertain outcomes do not trigger a solver change. See the
[live solver comparison](solver-comparison.md).

## Configuration

Admin stores each EVA account and its Outlook OAuth details in SQLite, alongside
global solver keys and the proxy list. No Outlook password is needed. Existing
`.env` and state files are imported once. See [storage](storage.md).

The in-process scheduler checks every 30 seconds, prepares up to two accounts at
a time within the global limit, and reverifies ready idle sessions every five
minutes. Transient failures back off; credential/OTP failures and uncertain login
outcomes require an explicit Prepare action. Flight searches remain on demand.

## Modules

* `internal/providers/eva`: member login, fresh form state, session reuse,
  availability parsing and Miles & Cash price preparation.
* `internal/outlook`: OAuth refresh-token rotation and read-only IMAP OTP lookup.
* `internal/antibot`: Hyper Akamai/adaptive/Imperva adapters and AnySolver
  reCAPTCHA/image recognition.
* `internal/login`, `internal/jobs`, `internal/web`: status, queue, cancellation,
  local API and frontend.

`go run ./cmd/eva-login` runs a real login check using the same configuration.
`go run ./cmd/outlook-check` verifies OAuth and read-only inbox access without
printing email contents.

## Sessions and OTP

Verified cookies are saved as account-specific portable JSON records in SQLite.
Each record contains the account, cookie scopes/expiry, browser identity and sticky proxy.
Storage works on macOS, Windows and Linux without an encryption key.
Each session retains its original locked proxy identity and browser identity.
Fresh logins take an unused pool slot and a new locked proxy session.
Member sessions retire five minutes before the earlier of the EVAID cookie deadline
or configured maximum age (default 3h). The server or residential peer can expire
sooner; a cookie deadline is an upper bound, not a guaranteed lifetime. A saved session is checked against the member page before use.
An expired-login response or a failed proxy/IP preflight permits one fresh
authentication on a new locked proxy. This recovery occurs before submitting
credentials or pricing. Requests within a flow retain the same proxy. Cookie
snapshots retain only live cookie state, so repeated searches do not grow an
unbounded history file. Background checks and searches serialize per account.
IP verification uses ipify through the same sticky proxy and checks the result
against the saved pin. It does not depend on Hyper's IP-lookup permission or
send a solver API key to the IP service. Hyper still generates Akamai sensors.

Outlook refresh tokens rotate into per-mailbox SQLite state records, alongside
the email and OAuth client ID.
Mailbox checks use read-only IMAP and `BODY.PEEK`; they never send mail or mark
messages read. OTPs must match the sender, subject and configured recipient,
arrive after the login checkpoint, and remain within EVA's three-minute lifetime.

Credential and OTP POSTs are not automatically repeated after transport errors,
403s, or uncertain outcomes. If EVA explicitly returns an adaptive security
interstitial, the adapter verifies it and forwards only its fresh opaque
continuation wrapper. The server-mandated challenge wait is distinct from
arbitrary request spacing.

Before each availability form submission, EVA receives a fresh three-sensor
Akamai sequence on the saved session's proxy. An inherited cookie stop marker
does not shorten this booking sequence: the one-sensor shortcut reproduced
HTTP 403 for HND–SIN. Other providers retain early stopping using Hyper's SDK
cookie validation. No fixed delay or automatic credential/pricing replay is added.
Search failures include the stage and rejected EVA host in the frontend.
An explicit HTTP 403 during search preparation now automatically restarts the
booking flow up to twice, with the same verified member session and sticky proxy.
Each attempt opens a new form and obtains fresh CAPTCHA and security state;
the UI shows recovery progress within the original search job. Credentials,
OTP, pricing, uncertain outcomes and solver-access failures are not replayed.
Repeated provider rejection can still exhaust the bounded recovery budget.
The availability adapter also recognizes Akamai's "Challenge Validation" page.
After explicit challenge verification it reopens the search form and obtains
fresh form state and a fresh CAPTCHA token, with one bounded recovery attempt.
Imperva continuations are recognized by their form, including the alternative
"EVAAir / Additional security check" page. Its hCaptcha is solved through the
same sticky proxy, verified at the fresh same-origin endpoint, and only then
is the opaque continuation submitted. Repeated continuations are limited to two
attempts. Cookie domain and renewal expiry are honored; legacy cookie scopes
are removed before installing a new security token.
If a proxy connection fails during search preparation, one fresh sticky session
and login may replace it. This recovery is restricted to availability and
security stages; credential, OTP, and pricing requests are never replayed.
Connection failures before a fresh login submits credentials use the same
bounded recovery. Saved-member read-only verification retries a transient
connection failure or explicit HTTP 403 once on the existing client. Repeated
failure can trigger the single fresh sticky-session renewal. An explicit 403
before credentials also permits that renewal; credential/OTP submissions do not.
Public login-check status, stage, code and duration are logged so another click
does not erase the diagnostic record. EVA's exact, explicit "Wrong CAPTCHA entry" rejection permits
one new image-CAPTCHA attempt. Wrong passwords, OTP failures, HTTP 403 and
uncertain credential outcomes do not permit that retry. The renewal and image
retry budgets allow at most three session attempts within the overall time limit.
Two corrected HND–SIN searches for 26 November 2026 returned all four connecting
itineraries with complete pricing in 76 and 70 seconds on the same saved session.

## Flight prices

Complete identical searches are cached in JSON for two minutes by default.
The frontend labels cache hits and provides a live-refresh bypass. CAPTCHA
solving overlaps with booking security setup. See [speed and caching](performance.md)
for the freshness rules, measurements and remaining bottlenecks.

Current verification note (20 October 2026): five consecutive searches passed
across economy, premium economy and business, including fresh login/Outlook OTP
and repeated HND–SIN searches. Hyper Akamai access was restored after the balance
was topped up. Reese84 access was also restored on 21 October; the comparison
linked above selects Hyper first with AnySolver as a service-failure backup.
A local website test returned two HND–SIN options for 27 November
after a manual retry of an availability 403; this prompted the automatic search
recovery described above. See the [results screenshot](screenshots/eva-results.png).
AnySolver Akamai fallback trials were rejected by EVA and were removed. The alternate hCaptcha
verification branch has regression coverage but still needs successful live
confirmation when EVA presents it again.

This is the captured `IS_MILES_MODE=false` cash booking flow, not award-ticket
redemption. Availability is parsed once for all returned flights and fare
families. Shared fare recommendations are calculated once.
Each subsequent fare family refreshes availability inside the same booking
session and matches the flight by its actual segments rather than stale IDs.
If a fare changes or disappears during this refresh, pricing can adopt the
new snapshot twice within the existing time/request limits. It retains finished
quotes only for unchanged segments, fare family, cabin and cash price; changed
fares receive fresh calculations. A failed later step preserves retrieved
flights and finished quotes in the API and frontend as partial results, with
the failure still visible. It never counts as a complete success.
The configured member profile supports one adult. Pricing requests allow up to
90 seconds, ordinary requests 40 seconds, and the full search ten minutes.
Login-only checks retain their six-minute limit. These are time limits, not delays.

The pricing adapter selects a fare, reads the current authenticated member's
traveller/contact profile and submits the captured catalog and price-preparation
steps, including `ACTION=BOOK`. This flow was explicitly authorized by the user
for testing and normal searches after consulting EVA support. No payment details
or ticketing endpoints are used. No captured personal details or request state
are replayed. Incomplete member profiles stop pricing with a clear error.

The frontend aggregates full cash fares and Miles & Cash maximum-offset
estimates without account-selectability notices or balance-based filtering.
The JSON retains provider selectability metadata for reference; it does not
control the aggregated maximum. Missing conversions remain explicit. Other-carrier
codeshares are not assigned a Miles & Cash quote. Missing conversions produce
partial results, never zero-mile award offers or an invented conversion rate.

## Verification status

Offline tests cover captured availability, route/date mismatch detection,
cash-versus-miles labeling, fresh login fields, OTP parsing, and local API
CSRF/cancellation behavior. The supplied capture contains ten economy flight
options and seven business options.

Search jobs and login checks now retain sanitized `recoveries` records after
completion or failure. These distinguish clean successes, successes after
automatic recovery, and final failures. Read-only GET response truncation and
temporary CAPTCHA task-status failures receive one retry; CAPTCHA creation
and credential/OTP/checkout submissions are not blindly repeated.
See [the 21 October reliability measurements](eva-reliability.md).

Live EVA login has produced a verified saved session. Both the CLI and frontend
successfully reused it; the frontend check took 3.9 seconds. OAuth/IMAP access and
Hyper Akamai generation have also been verified.

AnySolver's Reese84 sensor task has cleared the live booking handoff. The pricing
requests carry the navigation session ID from the page initializer and follow
updated AJAX session IDs; the analytics session ID is different.

Live TPE–HKG searches on 24 November 2026 completed with all eligible fare quotes:
economy returned ten flights (seven eligible, three codeshares) and 21 quoted
flight/fare combinations in 76.9 seconds; business returned seven flights and
21 quoted combinations in 72.9 seconds. Economy Basic was 14,070 miles plus
TWD 3,042; Standard 38,640 plus TWD 3,042; Up 80,430 plus TWD 3,042. These maximum
quotes were not selectable for the test account.

Premium economy on TPE–LAX for the same date returned three flights and all nine
fare combinations in 91.6 seconds. Basic was 207,900 miles plus TWD 6,359;
Standard 295,470 plus TWD 6,359; Up 415,380 plus TWD 6,359.

Live preflight renewal detected an unavailable saved proxy, started a fresh
sticky session and completed login with Outlook OTP. The stored cookie
snapshot was about 22 KB; subsequent searches successfully restored it.
