# Solver calls, bandwidth and operating costs

Audited against the implementation and public price pages on 22 October 2026.
Counts below describe one successful attempt. Recovery, expired sessions and
additional challenges add work. They are not invoice measurements.

## What one flow calls

| Flow | Hyper generation calls | AnySolver solving tasks |
| --- | --- | --- |
| Cached flight search | 0 | 0 |
| Healthy saved-session preparation | Normally 0 | Normally 0 |
| Fresh member login | 1–3 Akamai sensors for initial preparation; add 1–3 for each adaptive continuation and 1 hard / 2 passive SBSD generations when encountered | 1 reCAPTCHA v3 for login; another if the OTP form supplies a site key; 1 image recognition if shown |
| Register and verify membership | 1–3 initial Akamai sensors; add adaptive/SBSD work when encountered | 1 registration reCAPTCHA v3; 0–1 fallback image task |
| Onboard all the way to a saved login | Registration work above **plus a fresh member login** | Usually budget 2–3 v3 tasks plus 0–2 image tasks across both stages |
| Live flight search with healthy saved member session | 3 mandatory booking Akamai sensors; 0–2 Reese84 generations for supported Imperva continuations; add adaptive/SBSD work when encountered | 1 booking reCAPTCHA v3; additional hCaptcha only if presented; Reese84 task only when using AnySolver for Imperva |

Adaptive proof-of-work itself is calculated locally by the SDK, followed by its
fresh sensor preparation. `Hyper.VerifyConnection` is an IP check through ipify,
not a paid Hyper generation. It must not be counted as a Hyper API solve.

The live setting is `EVA_IMPERVA_SOLVER=auto`: try Hyper first, then AnySolver on
recognized solver-generation failures. A healthy Hyper Reese84 path does not also
create an AnySolver Reese84 task. Existing member cookies can avoid fresh login;
they do not eliminate fresh booking CAPTCHA/Akamai work for an uncached search.

Pricing adds no routine solver task per fare. It makes four dependent EVA calls
per distinct quote group (fare, travellers, catalogue, checkout price), with
availability refresh between groups. Group deduplication means ten displayed
flights do not necessarily mean ten separate quotes. Recovery/replanning can add
requests and sometimes restart protected search setup.

Read: [login.go](../internal/providers/eva/login.go),
[registration.go](../internal/providers/eva/registration.go),
[search.go](../internal/providers/eva/search.go),
[sensor.go](../internal/antibot/sensor.go),
[sbsd.go](../internal/antibot/sbsd.go),
[sec_cpt.go](../internal/antibot/sec_cpt.go),
[handoff.go](../internal/providers/eva/handoff.go).

## Solving tasks versus HTTP requests

A Hyper generation is one SDK API invocation; its generated payload then needs a
separate submission to EVA through the proxy. These are different requests.

AnySolver creates one task and polls for its result. One task is not one HTTP
request or one charge per poll:

- reCAPTCHA v3: one createTask plus result polls every three seconds.
- Text recognition: one createTask, then two-second polls unless immediately ready.
- hCaptcha: one createTask plus five-second polls.
- AnySolver Reese84: one createTask, then two-second polls unless immediately ready.

For example, a v3 task ready on the fifth poll means six API requests for one
solving task. A transient polling request may retry once. Creation is not blindly
replayed. Provider fallback inside one AnySolver task also affects actual billing;
the returned `cost` and billing export are authoritative.

## Proxy traffic estimate

The Go client requests forms, required security scripts and pricing payloads. It
does not load browser images, stylesheets, city catalogues or analytics bundles.
Whole-browser transfer totals therefore overstate our application's usage.

Available request-flow samples show approximately 0.20–0.28 MB for the four quote
calls plus an availability refresh, before allowance for variation and headers.
A practical application-payload model is:

```text
live search ≈ 0.6–1.5 MB setup + 0.25–0.40 MB × distinct quote groups
```

Six groups therefore suggest roughly 2.1–3.9 MB before retries, extra challenges,
TLS overhead and solver-side proxy traffic. The ranges below add planning room:

| Successful flow | Provisional proxy budget | Per 1,000 flows |
| --- | ---: | ---: |
| Fresh login | 1–3 MB | 1–3 GB |
| Full onboarding including saved login | 3–6 MB | 3–6 GB |
| Live search, roughly 6–20 quote groups | 3–10 MB | 3–10 GB |
| Cached search | 0 external proxy MB | 0 GB |

These are engineering estimates, not measured Evomi billable bytes. Cached
responses, compressed versus decoded sizes, unknown partial transfers and different
fare counts make the source samples unsuitable as an exact meter. Large searches
or retries can exceed the ranges. MB/GB here use decimal units.

Solver API JSON goes directly from the app to the solver, not through the EVA
proxy. But reCAPTCHA/hCaptcha tasks receive the sticky proxy: their workers may
consume additional residential traffic outside our client's visibility. Reese84
generation is proxyless; the returned payload is submitted to EVA by our client.
Outlook OAuth/IMAP traffic also goes directly, not through the EVA proxy.

For an accurate production budget, reconcile Evomi usage deltas over a controlled
batch with maintenance paused and no other consumers, plus solver task billing.
Application response-body counters alone cannot measure provider-worker traffic
or prove the proxy invoice total. No such isolated billing experiment was run here.

## Published unit prices

[Hyper](https://hypersolutions.co/#pricing): pay-as-you-go is EUR 3 per 1,000
generations across vendors. Advertised subscriptions start at EUR 1/1,000 for
Akamai and EUR 1.50/1,000 for Imperva; commitments and vendor-specific allowances
matter. The calculations below use pay-as-you-go, without currency conversion.

[AnySolver](https://anysolver.com/pricing) publishes starting rates of USD
0.52/1,000 v3, 0.10/1,000 text recognition, 0.65/1,000 proxied hCaptcha
(`PopularCaptchaToken`), and 1.80/1,000 Reese84 sensor tasks, plus a 5% top-up fee.
Those are catalog floors, not promises for our priority routing. Our v3 routing
prefers CapSolver then TwoCaptcha; image recognition prefers CapSolver then
CapMonster. For a conservative normal-route model, use
[CapSolver's pricing reference](https://docs.capsolver.com/en/pricing/):
USD 1/1,000 v3 and 0.40/1,000 image tasks, then add AnySolver's 5% funding fee.
[TwoCaptcha](https://2captcha.com/pricing) lists USD 2.99/1,000 for v3 scores above
0.3, so fallback can cost more. Actual task costs take precedence.

[Evomi Core Residential](https://evomi.com/pricing): USD 0.99/GB pay-as-you-go
from 15 GB; USD 0.79/GB from 1,000 GB; or USD 49/month for 100 GB with extra GB
at USD 0.49. This code uses the Core Residential endpoint, but does not know your
purchased plan. Expert filters can increase rates. Its
[proxy instructions](https://docs.evomi.com/proxy-instructions/) specify a
512-byte minimum per request. Use your actual dashboard rate for budgeting.

## Example cost scenarios

These intentionally state the assumed counts rather than imply one fixed price.
All amounts are **per 1,000 successful flows**, before retries, maintenance,
account supply, hosting, taxes and additional solver-worker proxy traffic.

| Scenario | Assumed Hyper calls per flow | Assumed AnySolver tasks per flow | Estimated PAYG cost per 1,000 |
| --- | ---: | --- | --- |
| Fresh login with adaptive security and OTP | 6 Akamai | 2 v3 + 1 image | EUR 18 + USD 3.51–5.49 |
| Full onboarding with adaptive security at signup and login | 12 Akamai | 3 v3 + 2 image | EUR 36 + USD 6.96–9.93 |
| Live search with one Hyper Reese84 solve | 3 Akamai + 1 Reese84 | 1 v3 | EUR 12 + USD 4.02–10.95 |
| Cached search | 0 | 0 | Zero marginal solver/proxy cost |

USD totals use the proxy MB ranges above at USD 0.99/GB and the CapSolver-based
AnySolver assumption including its fee. For example, 1,000 live searches use
4,000 Hyper generations (EUR 12), one v3 task each (USD 1.05 total), and 3–10 GB
of proxy data (USD 2.97–9.90). A second Hyper Reese84 generation adds EUR 3/1,000
searches; one AnySolver Reese84 instead adds at least USD 1.89/1,000, with Hyper's
Reese84 charge removed only if no Hyper generation was billable. An extra proxied
hCaptcha adds at least USD 0.6825/1,000 plus any worker proxy traffic.

Unsuccessful application flows can still consume paid, successfully generated
security responses. Cost per successful search is total batch spend divided by
successful searches, including failed/recovered attempts. Do not estimate it by
charging only the final happy-path requests.

Background health checks normally need no solve, but renewed logins do. At the
maximum default usable lifetime of 175 minutes, a continuously maintained fleet
needs roughly 8.23 fresh logins/account/day even before early revocation. Actual
expiry can be earlier. Fifty accounts imply roughly 411 renewal logins/day: under
the six-Hyper-call scenario that is about EUR 7.40/day in Hyper alone, plus CAPTCHA
and proxy costs. This is a capacity/cost model, not an observed daily bill.

Caching, shared in-flight searches, healthy member reuse and avoiding unnecessary
enabled accounts are the largest predictable cost controls. Never reuse one-time
CAPTCHA answers or mutable carts merely to reduce the invoice.
