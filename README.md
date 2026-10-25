# Awarddesk

A request-based EVA Air award-flight scraper with structured Go results and an
embedded web interface. EVA was chosen for its authenticated award availability,
mileage quotation and challenging session/security handoffs.

## Reverse engineering

Browser DevTools was used to trace requests, redirects, cookies, hidden form
state and embedded JSON. Repeating the journey with different dates, cabins and
passenger counts separated fixed inputs from session-specific values.

The default **Award tickets** flow checks the weekly availability grid for the
requested party, matches bookable itineraries, then retrieves each itinerary's
per-person mileage and multiplies it by the adult count. Fresh form state is used
for every selection. Waitlists are excluded; unknown response layouts are errors,
not empty inventory. The flow stops before passenger entry or reservation creation.

The main challenges were separate member/booking sessions, rotating encrypted
handoffs, delayed email OTP, security pages returned as ordinary HTML, mixed cabins
and discrepancies between availability and booking inventory. Account failover,
bounded retries and exact recent-quote reuse handle recoverable failures without
replaying uncertain credential or OTP submissions.

Go uses `tls-client`/`fhttp` for isolated HTTP sessions, HTML/JSON parsers, Hyper
for Akamai and Imperva, and AnySolver for CAPTCHA/security fallback. Outlook OAuth
and read-only IMAP retrieve fresh verification mail. SQLite stores configuration,
sticky proxies, reusable sessions, results and history. Production flows use no
browser automation. See the [flow walkthrough](docs/eva-awards.md) for implementation
details. Airport metadata comes from the public-domain [OurAirports data](https://ourairports.com/data/).

**Miles & Cash** remains a separate optional mode. It estimates the maximum
mileage offset against a paid fare and its cash remainder; that remainder is not
an award-ticket tax quote.

## Setup and run

Requires Go 1.25.5 or newer on Windows, macOS or Linux. The frontend and SQLite
database are included; no separate frontend server or C compiler is required.

```sh
git clone https://github.com/Fredrik-Rafn/awarddesk.git
cd awarddesk
go run .
```

Open http://127.0.0.1:8080 and configure http://127.0.0.1:8080/admin.
The initial admin password is **admin1234**, with no username; change it in Admin.

1. Add funded `HYPER_API_KEY` and `ANYSOLVER_API_KEY` values in Settings. Hyper
   needs EVA domain access and the relevant Akamai/Imperva products enabled.
2. Add authenticated **Evomi** proxies in Proxies, one per line. The current
   adapter requires exactly one supported sticky tag in the password, for example
   `http://USER:PASSWORD_session-demo@core-residential.evomi.com:1000`.
   `host:port:user:password` format is also accepted. Other proxy providers are
   not currently supported by this adapter. Fresh sessions receive distinct
   strict sticky identities; saved sessions retain their original identity.
3. Add an EVA login/password and its Outlook mailbox, OAuth client ID and refresh
   token in Accounts. Outlook needs IMAP access; its ordinary password is unused.
4. Prepare an account and wait for Ready, then search on the main page. Background
   maintenance refreshes sessions and retries recoverable account failures. It
   pauses after one hour without searches and resumes on the next search; manual
   preparation remains available.

Configuration is stored privately in `data/awarddesk.db`. Existing environment
and proxy files are imported once; optional first-run placeholders are in
[.env.example](.env.example). Subsequent changes belong in Admin. Stop with
Ctrl+C; use `go run . -port 8090` for another port. For a password-protected
recruiter demo, follow [ngrok setup](docs/demo.md), or use `./start-demo.ps1` on
Windows after installing/authenticating ngrok. The demo also exposes `/admin`,
with its separate admin login; saved API keys are never returned to the browser.
For an independently hosted demo, follow [Railway deployment](docs/railway.md),
including the private SQLite transfer.

Offline checks spend no solver credits:

```sh
go test -p 1 ./...
go vet -p 1 ./...
```

Node enables the optional frontend regression suite; see [tests](tests/README.md).

## Verified output

The saved [award response](docs/examples/eva-award-result.json) records real
availability and mileage. Results are Go `award.Result`, `Itinerary`, `Segment`
and `Fare` values, serialized as JSON: route, date, actual segment cabins, airline,
flight numbers, available aircraft, mileage, fees and checked party size. Multiple
eligible itineraries remain separate. `availableFor` is the successfully checked
party size, not an invented exact remaining-seat count.

For example, the recorded TPE → SIN search on 27 November 2026, premium economy
for one adult, returned BR225 and BR215 at **20,000 miles each**, with separately
labelled cash-derived tax estimates of **TWD 3,047 ≈ US$93.49 per itinerary**.

On 23–24 October 2026, **200 distinct uncached searches completed with zero final
failures**, at up to 30 concurrent searches: 168 without recorded recovery and
32 after automatic recovery. There were 79 priced results (242 itineraries) and
121 valid empty results. Priced-search latency was 60.5 seconds median.
Cash-derived fee estimates with USD conversion covered 225 of 242
quotes; the other fees remained unknown. See the [benchmark summary](docs/benchmarks/award-200-2026-10-24.json).
After a route-confirmation fix, a separate replay recovered 14 of those 17 missing
estimates; three remained unknown after bounded CAPTCHA retries.

Recorded results are historical evidence, not a guarantee of future availability.
Detailed methodology and failures are in [verification evidence](docs/verification-status.md).

## Limitations and edge cases

- One-way award searches support economy, premium economy and business, 1–6 adults
  and dates within 360 days. Children, infants, round trips and first class are
  unsupported. Aircraft and exact seat counts can be absent.
- Unfunded accounts expose award mileage but not the subsequent award-tax quote.
  Verified `taxes` therefore stay unknown. Where an exact matching cash itinerary
  is available, `taxEstimate` contains separately labelled cash-derived charges,
  optionally converted to USD with the original amount, rate and date retained.
  These are estimates, not verified award charges. Missing quotes are never zero.
- Complete results are cached for 30 minutes by default and can become stale.
  Sessions may expire early or lose their sticky IP. Retries have finite deadlines;
  upstream changes, unavailable accounts or solver/proxy failures can still fail.
- The app is one process with SQLite, bounded concurrent jobs and exclusive account
  use. The ngrok demo shares one workspace; it is not a tenant-isolated public
  service. Live use requires working accounts, proxies and funded solvers.
