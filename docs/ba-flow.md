# British Airways flow notes

The implementation was derived from the user's login and award-search network captures, then verified with new request-only sessions. Captures are development evidence only; the shipped application never loads them.

1. Start an isolated TLS-client session (Chrome 152 profile and matching UA/client hints/header order), optionally through one sticky authenticated HTTP proxy. Generate OAuth state, nonce and PKCE verifier with crypto/rand.
2. GET `https://accounts.britishairways.com/authorize` with the BA public client, Avios audience and S256 challenge. Follow bounded redirects and parse fresh hidden login state and the hCaptcha site key.
3. Ask AnySolver for a `PopularCaptchaTokenProxyLess` token. Poll with a four-minute deadline and bounded provider retry/fallback. Submit the account credentials and CAPTCHA once to the fresh `/u/login` URL.
4. Follow authentication redirects, require an exact callback URL and matching OAuth state, then exchange the code/verifier at `/oauth/token`. Confirm `/userinfo` returns an account subject. Refresh once for the current access/ID tokens. These are retained in memory only.
5. GET the BA pre-login page. Parse the current Akamai script and fresh `_abck` / `bm_sz` cookies. Detect outbound IP through the same HTTP session/proxy, generate Hyper sensor data and submit at most three sensor payloads. The optional SBSD path is bounded separately. A successful sensor response alone is not treated as successful login/search.
6. GET `/nx/b/bff/customer-account/v0/csrf`; its token is plain text. POST `/getClassicTokens` with access/ID tokens, `Authorization: Bearer ...`, CSRF and BA application headers. The Authorization header was absent from the sanitized HAR but was required in the working request flow.
7. Establish the classic booking session via the public redemption page and `/travel/loginr/public/en_dk?eId=109010`. Store returned classic token cookies in this session's jar. Parse the fresh authenticated search form and FormToken.
8. POST `/travel/redeem/execclub/_gf/en_dk` with `eId=100002`, `WebApplicationID=BOD`, route, date DD/MM/YY, one-way, adult count and cabin M/W/C/F. Extract the interstitial form and its event `111011`. An empty FormToken on this interstitial is intentional; do not reuse the earlier token.
9. Parse selectable journey rows and segment times, carriers, flight numbers, route and seats shown for the requested cabin. A high-demand/error page is an error, not zero availability.
10. POST the exact live selection value with pricing event `100004` to the `/device-all` endpoint. Carry the current route flags and rotating `data-formToken` between quotes. Parse Avios + cash currency, and every exposed option in the additional-price list. Check advertised option count against captured options.
11. Follow only same-host, same-path read-only detail links for event `100023`; extract explicitly labelled aircraft/operator data when supplied. Cap and cache these optional requests. No Continue/payment/booking event is submitted.

## Expiration and reliability

OAuth tokens, classic booking tokens, CSRF/form tokens, Akamai cookies and CAPTCHA tokens all have session or short-lived validity. Replaying any from a HAR would eventually fail. This application regenerates them for each search, consumes CAPTCHA once, and updates rotating pricing tokens. It does not rely on the user's logged-in browser. Session rejection stops the job; a subsequent user search creates an entirely new session.

A stable proxy address string is insufficient if its provider rotates the exit IP between requests; configure a sticky session with that provider. When configured, that proxy is also passed to the CAPTCHA task. Without a proxy, the CAPTCHA worker is not guaranteed to share the BA session's IP. BA may change its acceptance policy, require MFA, alter markup, or show a high-demand page. Those cases produce errors rather than unbounded retries. Historical SBSD submissions were accepted, but were not proven to resolve BA's high-demand page; that page may also reflect service conditions.

Only the basic search/pricing flow is verified. The UI exposes four cabin mappings and up to nine adults, but live validation has so far covered one adult, economy, JFK–LAX. Fare names such as Saver/Standard are not invented when BA does not label them. Prices remain estimates until BA's later booking flow.
