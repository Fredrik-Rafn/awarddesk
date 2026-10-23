# EVA award tickets

The main page defaults to **Award tickets**. **Miles & Cash** remains available
as a separate mode using the existing implementation. Award searches use
the member award availability and mileage-quotation pages. The API
selects it with `"mode": "award"`; omit `mode` for the original flow.

```json
{
  "provider": "eva",
  "origin": "SIN",
  "destination": "TPE",
  "date": "2026-11-24",
  "cabin": "economy",
  "adults": 4,
  "mode": "award",
  "refresh": true
}
```

The award flow accepts one-way searches, economy/premium economy/business, 1–6 adults
and dates within 360 days. These are award-form limits; Miles & Cash keeps its
existing limits. A date or cabin unavailable on a route can return an empty
result rather than a request failure.

## Request lifecycle

1. The normal job queue leases an account exclusively. The member session,
   browser identity and original sticky proxy are restored or renewed using the
   existing session manager. Different accounts can work concurrently.
2. Open `booking.evaair.com/flyeva/eva/b2c/plan-your-journey/award-upgrade-availability/award-upgrade-availability.aspx`.
   If the award portal needs a separate login, use its fresh form, CAPTCHA and
   Outlook OTP. Save the resulting cookies with the member session afterward.
3. Prepare the availability page's Akamai state, then submit the requested route,
   date, award cabin and **actual adult count**. Availability cabin codes are
   `EY`, `PE` and `SD`; the inquiry type is `award`, with `ONE_WAY` selected.
4. Read the requested date's status in the weekly availability grid. Accept only
   the explicit Available status; waiting lists are excluded. The response must
   confirm the route, cabin and party. A recognized unserved-cabin/route message
   is an empty success; an unexplained missing grid is a protocol error.
5. If no flights are available, finish immediately. Otherwise load EVA's airport
   directory (`getAirportCache.ashx?action=05`) when this account's cached public
   metadata is missing or older than 24 hours. Continent/community codes come from
   that response rather than a hardcoded route list.
6. Open the award portal's `select-itinerary.aspx`. Submit the route/date and award
   booking class (`X`, `E` or `I`). Forward its fresh `Parameter` form only to
   `select-first-journey.aspx` in the same award portal.
7. Match the returned itinerary cards against the requested date and the available
   flight sequences. Keep only available segments and verify route/times/cabins.
   When several quotes are needed, try reserving up to two spare ready accounts
   without waiting. Split exact itineraries between those helpers and the parent.
   Helpers can use the parent's private party-availability evidence when the
   complete query and every assigned segment match and the check is at most
   60 seconds old. This comes only from this search's live availability response,
   never a cached result. Otherwise they repeat the full availability check.
   Every helper loads fresh selection on its own account/proxy; no hidden forms
   or server-session cookies are shared. Party availability is attributed to the
   coordinator's check; mileage quotations can come from other accounts.
8. Submit one selected radio with that response's current hidden selection values
   and `btn_Confirm_Click`, preparing three fresh security rounds immediately
   before that submission. Forward its fresh handoff only to
   `mileage-deducted.aspx`. This is quotation: the integration never posts from
   the mileage page to passenger details or a reservation-completion step.
9. Read **Mileage deducted for one person**, verify that the quoted itinerary
   matches the selected flight, and multiply the integer mileage by the requested
   adults. Emit the priced itinerary immediately so the UI can show progress.
10. For another flight, try reopening the flight-selection page for fresh state.
    If EVA discarded that search, rebuild the itinerary query; after the first
    failed reopen, skip that shortcut for the rest of the search. Never replay an
    old ZIPSTATE or encrypted handoff. Every lane remains serial. Merge only exact
    validated helper quotes, and quote missing flights on the parent after helper
    failure. All helpers are joined/cancelled and leases released. No spare
    capacity means the original sequential path, with no nested capacity wait.
    Helper time is 45 seconds setup plus 35 seconds per assigned flight, capped
    at three minutes. It is shortened to preserve `30 seconds + 35 seconds ×
    assigned flights per helper × helper count` before the job's execution
    deadline for parent fallback; less than
    ten useful seconds skips the helper. One safe initial direct-setup failure
    can discard the proof and retry full availability/security. Authentication,
    unknown outcomes and already-quoted partial results cannot trigger that retry.
11. Optionally retrieve cash-fare tax estimates for exact matching flights. Keep
    them separate from actual award taxes; convert available amounts to USD.
12. Persist history and cache only a complete successful result, then release the
    account and proxy lease. Award and Miles & Cash queries have separate cache
    identities, as do different party sizes. The normal 30-minute TTL applies.

## Output meaning

### Tax quotation boundary

The inspected mileage-deduction responses do not contain a tax total or a tax
breakdown. A fresh live check with an unfunded account confirmed that EVA renders
the mileage source disabled and omits its next-step control entirely. A narrowly
derived confirmation postback with unchanged allocation/balance returned the
same page, without a tax quote. No passenger submission, reservation completion,
mileage purchase or payment was made in this investigation.

EVA's separate normal fare-selection response does expose cash-fare tax totals
and itemized components before passenger details. Its `Price.totalTaxes`,
`totalTaxesPerPax` and `listTaxes` are useful research inputs, but have not been
verified as the actual award cash charges. They must not be copied into award
`taxes` as a verified quotation. Origin market, routing, issuance date, currency,
surcharges and tax exemptions can affect equivalence. Unknown taxes remain
unknown in the award contract; the former generic disclaimer banner is removed.

The accepted fallback is implemented in `award_taxes.go`: use an existing member
session and open a fresh cash booking context. Prefer each exact matching
recommendation's explicit availability tax amount. Reconcile total = base + tax,
bound and recommendation totals, ADT price, currency and whole-party cash price;
reject missing/ambiguous values, dynamic discounts and unexplained service fees.
This retrieves estimates for all matching flights from one availability response.
Four live selected-fare checks across TWD/HKD and parties of four/two matched
those availability totals exactly. This validates the cash estimate units, not
equivalence to award taxes.

If availability does not supply a valid total, stop at the matching flight's FARE
response and reconcile its itemized charges. Match full segment sequence,
local times, route and cabin; skip
mixed-cabin awards rather than infer their fees. Validate the server-selected
itinerary and reconcile itemized per-adult taxes against the returned party total.
Store these only in `taxEstimate`, never verified `taxes`. `source` distinguishes
`eva_cash_availability` from `eva_cash_fare`; availability totals have no invented
itemized breakdown. A two-minute budget and
at most two fresh quotation contexts bound enrichment. Completed estimates survive
a safe retry. Failure leaves the award result usable, with unknown fees where
necessary; it never initiates another credential login for optional enrichment.

`internal/exchange` converts original totals to USD using daily USD-base rates
from ExchangeRate-API, cached in SQLite and shared across accounts. Exact decimal
arithmetic rounds once to cents. The result retains original currency, rate,
source and timestamp. Failed refreshes back off; unavailable conversion retains
the original amount. The UI labels these as cash-derived estimates and exposes
their breakdown and conversion details.

A live tax-only probe for TPE–HKG on 27 November 2026 retrieved BR891 and BR809
cash quotes without passenger entry, checkout, or Miles & Cash calculation. All
three economy families on each flight returned TWD 3,047 per adult: YQAC fuel
1,222, YRVB booking service 1,075, and TWAE airport service 750. Base fares differed
while these components matched. This is evidence for a potential cash-derived
estimate, not a verified award tax quote. The response also includes base-fare
and subtotal rows inside `listTaxes`; summing every row would double-count them.

### Mileage and availability

For award fares, `pricingType` is `award` and `points` is the **whole-party mileage**.
For example, 17,500 per person becomes 70,000 for four adults.

`availableFor: 4` means EVA's availability inquiry accepted four passengers for
that itinerary. It is not an exact remaining-seat count; `seats` stays absent when
the page does not expose one. Availability is unconfirmed inventory, not a held
or completed reservation. A test account's insufficient mileage does not prevent
reading the quotation or invalidate the independent party availability inquiry.

The mileage page does not supply taxes/fees in the observed flow. Verified
`taxes` remain empty. When a matching cash-fare breakdown is available, the UI
shows its separately labelled `taxEstimate`, preferably converted to USD;
otherwise fees stay unquoted. It never displays an unknown charge as zero or
uses a Miles & Cash remainder as taxes. `complete` describes completion of the
availability/mileage flow, not a taxes-inclusive ticketing quote.

Individual segments retain their actual cabin. A connection in a lower cabin
does not get relabeled as the requested higher cabin. At least one segment must
match the requested cabin; quote verification checks each segment again.
EVA may offer an airport change, such as arrival at Taipei Songshan (TSA) followed
by departure from Taoyuan (TPE). Both airports are preserved and a self-transfer
warning states that ground transport is not included. Wall-time connection order
is compared only when the connecting airport is the same.

## Recovery and safeguards

### Status interpretation

The status audit covered both inspected request flows and 25 local response
captures. Availability cells exposed `Available` with `BR.png`, fullwidth-W
`Ｗaiting` with `Triangle.png`, and empty image placeholders without `alt` or
`src`. Booking segments exposed `status Available(Unconfirmed)` and
`status waiting`. No other status variant was observed in those responses.

Only explicit availability qualifies a flight. Waitlists and the precisely
recognized empty placeholder provide no availability evidence, so those flights
are excluded without hiding positively available siblings. The blank placeholder
does not establish a definitive sold-out or not-operated reason. Known negative
text variants also have regression coverage.

Unfamiliar nonempty labels/images and malformed status structures remain protocol
errors. They are not silently interpreted as unavailable or omitted from a
supposedly complete result. Warnings log the route/date/cabin/party, a bounded
single-line label and image filename only; signed URL queries, private form state
and full response bodies are excluded. These diagnostics support investigation
and a targeted parser regression before accepting a newly understood variant.
An old no-service notice cannot override an existing valid availability grid.

### Retry boundaries

- HTTP transport retains its bounded read-only recovery and deadline handling.
- Explicit Akamai wrappers returned with 403/428 can continue once after solving,
  using only their fresh opaque wrapper on the same expected award page. An
  ordinary 403 does not authorize replaying the original POST.
- A mismatch between the availability and booking systems before any mileage
  quote triggers one fresh availability check. It is not cached as empty.
- Safe award search/quotation failures can use up to **five distinct accounts**
  and their sticky proxies. This broader policy applies only to the award flow,
  which stops before reservation submission. The original flow keeps its policy.
- Uncertain credential/OTP outcomes are excluded from this failover. An explicit
  incorrect-image-CAPTCHA response or HTTP 403 credential rejection can use a
  different account; the rejected account's credential request is not replayed.
  Read-only login-page failures and mailbox checks before credential submission
  can also move to a different account.
  Adaptive security work before forwarding an explicitly intercepted request is
  also restartable on another account; a lost authentication forwarding response
  remains uncertain and is excluded.
  Overall queue/execution deadlines and cancellation still bound the work.
- Protocol, unsupported-route and inventory errors do not mark a healthy login as
  repeatedly broken. Authentication, session and transport failures still affect
  account recovery/cooldowns.
- If all recoveries fail, preserve the best validated partial result, explicitly
  incomplete. Do not merge old inventory into a fresh complete result, cache a
  partial result or invent availability/mileage to report success.
- During the same search's account recovery, retain an exact award mileage quote
  for at most five minutes. First repeat actual-party availability and obtain a
  fresh matching itinerary; compare every segment's flight, airports, local times
  and actual cabin, plus query and party size. Only mileage is reused, never old
  inventory or hidden booking state. Missing or changed flights are not retained.
  The recovered result keeps the oldest quote timestamp, so another recovery
  cannot extend its reuse window or cache lifetime.

## Efficiency and diagnostics

Only functional HTML/forms and security dependencies are requested. Browser
assets, analytics, duplicated airport-directory fetches and the Miles & Cash
traveller/catalogue sequence are skipped. A recognized empty availability result
needs no mileage requests. Login state and complete search results use the
existing SQLite storage and cache.

The two observed selection handoffs submit immediately; their JavaScript page-load
timers are not copied as fixed sleeps. Dependent form operations remain sequential
within one account. Separate searches and optional quote helpers use separate
accounts. Helpers respect the same global account capacity, refuse fresh login,
and never recursively request more helpers or cache their subset as a full result.
They can fall back when a member-ready account lacks award-portal authentication.
Fresh parent inventory evidence avoids three repeated availability Hyper calls
per already-authenticated helper in the normal path. Full helper availability
fallback adds those calls when needed. Each submitted flight still requires its
own three selection sensor rounds, on whichever account quotes it. Normal tax
enrichment uses just one parent cash booking context.

The first captured direct-flight path requires eight application requests after
authentication, including the airport-directory fetch. A later quote can use three
more requests when reopening the selection page works, or rebuild the query when
it does not. Security, login, redirects and recovery add requests. Each distinct
itinerary is quoted; equal mileage observed on two flights is not assumed to prove
that every itinerary has the same cost.

Stage history records quote progress and recoveries; sanitized request logs record
page paths and durations without form values. The diagnostic CLI accepts award
queries and counts their quotes and time to first quote. Live benchmark reports
are private workspace artifacts; published summaries distinguish exploratory runs
from the final tested build, empty results from priced results, and first-pass
success from success after automatic recovery.

For local protocol diagnosis only, `AWARDDESK_AWARD_DEBUG_DIR` can save failed
response bodies to a private directory. They can contain personal information or
live form state. It is off by default; never publish these files or enable this
on a shared demo. Sanitized fixtures are separate from those raw captures.

Implementation: [search orchestration](../internal/providers/eva/award_search.go),
[availability](../internal/providers/eva/award_availability.go),
[quote parsing](../internal/providers/eva/award_pricing.go),
[portal authentication/security](../internal/providers/eva/award_portal.go),
[account recovery](../internal/fleet/award_recovery.go).
Tests use sanitized HTML structures and fake navigation; they do not spend solver
credits. Live evidence is tracked separately in [verification status](verification-status.md).
