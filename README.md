# Awarddesk

A request-based EVA Air scraper with an embedded web UI, reusable login sessions
and structured Go results.

## Airline and scope

EVA Air was chosen because its Miles & Cash journey combines authenticated flight
availability, multiple fare families and mileage pricing across several booking
systems. The implementation supports one-way searches for 1–9 adults in economy,
premium economy and business, returning all eligible options exposed for the query.

**This integration implements Miles & Cash (`IS_MILES_MODE=false`), not EVA's
separate award-ticket redemption flow.** It reports the maximum mileage offset
and remaining cash for the entire party, independent of the account's balance.
The cash remainder is not a taxes-only amount; reported seats are paid-fare
availability, not award-seat inventory. Estimates are explicitly marked, with a
separate flag indicating whether the account can select the quoted offset.

## Reverse engineering

Browser DevTools was used to inspect the request sequence, redirects, cookies,
form fields and embedded JSON, separating fixed inputs from session-specific state.
The resulting Go implementation uses HTTP requests throughout, without browser
automation:

1. Initialize member login, complete security checks, submit credentials and confirm
   authenticated state. When required, Outlook OAuth and read-only IMAP retrieve
   a fresh email verification code.
2. Restore the saved cookies, browser identity and original sticky proxy for later
   searches; create a fresh booking context for each uncached search.
3. Submit route, date, cabin and passenger count. Follow the booking handoff and
   parse flight segments, fare families and reported seats from availability data.
4. Select each distinct fare and follow traveller, catalogue and checkout-price
   preparation (`ACTION=BOOK`). Read `MilesAndCashView.maxRankAvailable` for its
   mileage amount and cash remainder. No payment or ticketing request is submitted.
5. Normalize party totals into Go structs, omit explicitly ineligible fares and
   publish progress followed by the completed result.

The main challenges were rotating page tickets and recommendation IDs, separate
member/booking sessions, delayed OTP delivery and security pages that resemble
ordinary responses. Availability prices are per adult while checkout prices are
party totals; normalizing those units prevents false price-change errors.

`tls-client`/`fhttp` maintains isolated HTTP sessions; HTML and JSON parsers extract
fresh state. Hyper handles Akamai and Imperva/Reese84, with AnySolver for CAPTCHA
and configured security fallback. Stage timings, diagnostic commands, local
fixtures and `httptest` support investigation and regression testing. See the
[request-flow walkthrough](docs/code-walkthrough.md) for functions and endpoints.

## Setup and run

Requires Go 1.25.5 or newer on Windows, macOS or Linux. No separate frontend server,
external database or C compiler is required.

```sh
git clone https://github.com/Fredrik-Rafn/awarddesk.git
cd awarddesk
go run .
```

Open http://127.0.0.1:8080 for searches and http://127.0.0.1:8080/admin for setup.
The initial admin password is **admin1234**, without a username; change it in Admin.

1. In Settings, enter funded `HYPER_API_KEY` and `ANYSOLVER_API_KEY` values. Hyper
   must permit the EVA domains and the Akamai/Imperva products used by the flow.
2. In Proxies, add sticky HTTP proxies, one per line:
   `http://user:password@host:port` or `host:port:user:password`.
3. In Accounts, add an EVA login/password, Outlook mailbox, OAuth client ID and
   refresh token. The mailbox needs IMAP access; its ordinary password is not used.
   The EVA member profile must have the contact details required for pricing.
4. Prepare the account and wait for Ready, then submit a search on the main page.
   Background maintenance keeps sessions prepared while the app is running.

Configuration, sessions, cached results and history live in `data/awarddesk.db`.
Existing `.env`, proxy and session files are imported once; subsequent changes
belong in Admin. Keep the database private: it contains credentials and tokens.
Stop with Ctrl+C; use `go run . -port 8090` for another port.

For a shared demo, Windows users can run `./start-demo.ps1` instead; it prints an
HTTPS URL and separate demo credentials. Admin stays localhost-only. See
[ngrok setup](docs/demo.md) for prerequisites and macOS/Linux instructions.

Offline checks require no airline credentials or solver credits:

```sh
go test -p 1 ./...
go vet -p 1 ./...
```

Node is optional for the frontend test suite; [test instructions](tests/README.md)
explain its scope.

## Verified output

Recorded on 22 October 2026: HKG → TPE, 10 December 2026, economy, two adults.
The search completed in 131 seconds with seven itineraries and 21 priced fare
entries. A [complete saved response](docs/examples/eva-result.json) is included;
it contains historical results, not a current availability guarantee.

Example from that response: BR892, Boeing 787-10, Economy Basic, nine reported
seats, **29,820 miles + HKD 1,388 for two adults**. The API represents the estimate
as:

```json
{
  "pricingType": "miles_cash",
  "estimated": true,
  "milesCash": {
    "miles": 29820,
    "remainingCash": { "amount": "1388.0", "currency": "HKD" },
    "selectable": false
  }
}
```

Results use `award.Result`, `Itinerary`, `Segment` and `Fare` structs, serialized
as JSON by the API. For this product, use `milesCash`; the generic award-only
`points` and `taxes` fields are not populated. `selectable: false` means the saved
account could not select that maximum offset, not that the calculation is an
award quote. Multiple fare families remain separate entries.

## Known limitations and edge cases

- Actual award-ticket prices and award-seat inventory are not implemented.
  Round trips, children, infants and first class are unsupported. Aircraft and
  seat counts can be absent; displayed seat counts may be capped by EVA.
- Pricing depends on mutable airline state and response times. More than 30
  distinct fare groups yields an incomplete result; incomplete/failed results
  are not cached. Genuine price changes and unknown response formats can fail.
- Complete searches are cached for 30 minutes by default; results may become stale.
  Sessions can be revoked before their estimated expiry or lose their sticky IP.
- Retries and account failover are bounded. Uncertain credential, OTP and checkout
  outcomes are not blindly replayed. OTP delivery and proxy/solver availability
  can delay or prevent completion.
- The 500-search baseline completed 423 jobs (84.6%, including 21 valid empty
  results). After correcting per-adult/party comparisons, seven targeted searches
  passed; the full benchmark has not been repeated. TPE–ICN handoffs and some
  missing booking-data responses remain unresolved. See [verification evidence](docs/verification-status.md).
- This is a single-process SQLite application with bounded concurrent workers and
  exclusive account use. The ngrok demo shares one workspace; it is not a
  tenant-isolated public service. Live use needs working accounts, sticky proxies
  and funded solvers; the latest changes were checked offline without paid calls.
