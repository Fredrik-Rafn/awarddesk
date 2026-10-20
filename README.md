# Awarddesk

Local airline integration app. **Go backend + embedded HTML/CSS/JavaScript frontend.**
One process serves both at http://127.0.0.1:8080. There is no Node, npm, frontend
build pipeline, database, or separate frontend server.

| Module | Available functionality | Status |
| --- | --- | --- |
| EVA Air · Miles & Cash | Login, Outlook OAuth OTP, JSON session files, flight availability and checkout price preparation | Integrated and live-verified in economy, premium economy and business; one adult, one way |
| ANA Mileage Club | Fresh login, Hyper/Akamai initialization, authenticated member verification | Direct login previously verified; current direct check requires MFA; proxy checks return HTTP 403 |
| British Airways / Avios | Login and award-flight search | Experimental; repeatable standalone login remains unverified |

ANA award searches are not implemented. The background job saves verified ANA
sessions in per-account JSON files and validates them before reuse.
See [saved sessions](docs/sessions.md).

EVA also reuses complete identical search results for two minutes, marks them
as cached, and offers **Refresh live** to bypass them. See
[search speed and caching](docs/performance.md).

## Start backend and frontend

Supports **macOS, Windows and Linux**. Install Go 1.25.5 or newer.
For a new macOS/Linux checkout:

```sh
git clone https://github.com/Fredrik-Rafn/awarddesk.git
cd awarddesk
cp .env.example .env
cp proxies.example.txt proxies.txt
# Fill in .env and proxies.txt, then start backend and frontend together:
go run .
```

On Windows, from PowerShell:

```powershell
git clone https://github.com/Fredrik-Rafn/awarddesk.git
cd awarddesk
Copy-Item .env.example .env
Copy-Item proxies.example.txt proxies.txt
# Fill in .env and replace the proxy examples with your proxy credentials.
go run .
```

Credentials, solver keys, proxy credentials and saved sessions are excluded from
Git. Configure the new laptop's `.env` and `proxies.txt` separately. Sessions and
rotated Outlook tokens are readable JSON files under `.state/`, one per account.
They can be copied between operating systems, with no machine-specific encryption
or key. A copied airline session still needs its original sticky proxy and must
pass the provider's expiry and authentication checks. See [file storage](docs/storage.md).

Open **http://127.0.0.1:8080**. The ANA panel shows
progress, success/failure, and cancellation. The session job starts automatically;
**Prepare ANA session** also runs it on demand. No flight search runs automatically. EVA has its own session controls; its startup and periodic job is controlled by EVA_BACKGROUND_ENABLED. See [EVA integration](docs/eva.md).

Other modes:

```powershell
.\start.ps1                      # UI only, or use credentials already in the environment
.\start.ps1 -Provider ba         # Prompt for BA and solver credentials
.\start.ps1 -Provider all        # Prompt for both airlines
.\start.ps1 -Provider ana -Port 8090
```

The launcher runs current source every time, so an old executable cannot hide
code changes. Ctrl+C stops the server and cancels active jobs. If PowerShell
blocks scripts, use `powershell -ExecutionPolicy Bypass -File .\start.ps1 -Provider ana`
for this invocation, or use `go run .` with environment configuration.

## Configuration

The backend automatically loads `.env` from the current project directory at
startup. Restart after changing it. Existing process environment variables take
precedence. `.env` is excluded from Git; `.env.example` contains empty placeholders
for a new checkout. Values can be unquoted, single-quoted (literal), or
double-quoted (escaped); full-line `#` comments are supported. There is no variable
expansion or inline-comment syntax.

The optional PowerShell launcher can still prompt for overrides and restores
the previous process environment on exit. It does not modify `.env`.

| Variable | Used by |
| --- | --- |
| `ANA_MEMBER_NUMBER`, `ANA_PASSWORD` | ANA ten-digit AMC number and web password |
| `BA_EMAIL`, `BA_PASSWORD` | British Airways |
| `HYPER_API_KEY` | Akamai and EVA booking security |
| `ANYSOLVER_API_KEY` | BA CAPTCHA and EVA reCAPTCHA / image recognition |
| `EVA_EMAIL`, `EVA_PASSWORD` | EVA member account |
| `OUTLOOK_EMAIL`, `OUTLOOK_CLIENT_ID`, `OUTLOOK_REFRESH_TOKEN` | Read-only IMAP OAuth for fresh EVA OTP messages |
| `ANA_BACKGROUND_ENABLED`, `EVA_BACKGROUND_ENABLED` | Set false to disable automatic session preparation |
| `AWARDDESK_PROXY` | Optional sticky HTTP proxy shared by airline requests |
| `AWARDDESK_PROXY_FILE` | ANA / EVA Evomi proxy pool; takes precedence over `AWARDDESK_PROXY` for ANA |

Proxy formats: `host:port:user:password` or `http://user:password@host:port`.
ANA does not require BA credentials or an AnySolver key. Missing configuration
leaves the relevant action disabled. Credentials are stored only in the local
`.env`, not in Go/JavaScript source or the tracked example.

## Project overview

```text
main.go                    Server entry point and module wiring
cmd/ana-login/             Standalone ANA login verification command
internal/
  providers/ana/           ANA authorization, SSO and member verification
  providers/eva/           EVA login, session reuse, availability and Miles & Cash
  outlook/                 OAuth token rotation and read-only OTP mailbox reader
  providers/ba/            BA login, booking-session flow and result parsing
  antibot/                 Hyper Akamai adapter and AnySolver CAPTCHA adapter
  session/                 HTTP/TLS, cookies, proxy and safe retries
  proxies/                 Editable Evomi pool and exclusive locked-session leases
  sessionstore/            Portable JSON files and per-account process locks
  config/                  Local .env loading and environment precedence
  login/                   Background login-check lifecycle and cancellation
  award/                   Shared flight-search models and provider contract
  jobs/                    Bounded flight-search queue and result lifetime
  airports/                Embedded airport directory and autocomplete
  web/                     Local HTTP API and security boundary
    assets/                Frontend: index.html, app.css, app.js, sessions.js
 docs/                     Architecture, airline flows and verification scope
```

See [architecture and API](docs/architecture.md), [ANA login](docs/ana-login.md),
[BA request flow](docs/ba-flow.md), and [verification limits](docs/verification-status.md).

## Development and checks

```powershell
go run .                    # Serve backend + frontend; loads local .env
go test -timeout 45s ./...   # Offline tests, no airline requests or solver charges
go vet ./...
go build -trimpath -o bin/awarddesk.exe .
.\bin\awarddesk.exe -port 8080
```

Frontend files are embedded at Go compile time. After editing files under
`internal/web/assets`, stop and rerun `go run .`, then refresh the browser.
Backend changes also require restarting. A separate development web server
is unnecessary; API and UI use the same origin.

For a terminal-only ANA login check: `./login-ana.ps1`, or
`go run ./cmd/ana-login` with the three ANA/Hyper environment variables set.

## Limits

Localhost only; do not publicly reverse-proxy the app. POST/DELETE endpoints
require the per-process CSRF token from `/api/config`. Secrets stay on the server.
ANA permits one login check at a time with a 150-second total deadline.
Concurrent starts share the current check. A successful result is reused for
15 seconds with its original timestamp. Failures have a five-second cooldown
(one minute for account/authentication responses); upstream cooldowns are honored. MFA/browser handoffs
return explicit errors. Credential submissions are never automatically retried.
Individual HTTP requests have no fixed spacing; dependent steps remain
sequential. The optimized ANA flow was live-verified in 3.95 seconds with five
Hyper generation calls and one cached-per-session IP lookup.

The local `proxies.txt` contains the supplied 100 entries and is excluded from
Git. Edit one endpoint per line; changes reload on the next lease without
restarting. Blank lines and `#` comments are allowed. Invalid edits fail closed.
ANA uses a fresh Evomi `lockedsession` ID per attempt, retaining that endpoint
and cookie jar for the whole attempt. The original export remains unchanged.
Locked sessions do not support Evomi's lifetime parameter; the application
instead enforces its 150-second deadline and checks the exit IP before credentials.
Fresh login uses the next unused pool slot; restored sessions use their original
locked proxy ID. The current file has 100 one-day exports, but locked mode does
not use their lifetime parameter. A one-day local cache limit does not guarantee
ANA or the residential peer will remain available for that long.
See [proxy and reliability details](docs/reliability.md).

BA searches use a serial queue, bounded requests, and cancellation. Results are
in memory for one hour. EVA price preparation uses the member profile and the captured checkout step; no payment or ticketing is performed. Current
search scope is one-way, adults only, Denmark/English and DKK. Export JSON to
keep search results.

Historical HAR files, research scripts, diagnostic live tests, old reports,
compiled binaries, and duplicate launchers are not part of this source tree.
The previous project copy remains in its original Codex output folder.
Third-party notices are retained in `THIRD_PARTY.md` and `THIRD_PARTY_LICENSES.txt`.

