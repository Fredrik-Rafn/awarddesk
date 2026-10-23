# Share a demo with ngrok

The app keeps running on your computer. ngrok forwards a public HTTPS address to
its localhost port. Visitors sign in with a separate demo password. The admin UI,
admin API and manual account-preparation endpoints stay localhost-only.

## Windows: one command

Stop the current Awarddesk server with Ctrl+C first. From the repository root:

```powershell
.\start-demo.ps1
```

The launcher starts ngrok, reads its assigned HTTPS URL, generates a random demo
password and starts `go run .`. It prints the URL, username `demo`, password and
local admin address. Share the URL and demo credentials with your visitor.

Keep the terminal and computer running. Ctrl+C stops the app and the tunnel started
by this launcher. It does not stop other ngrok processes. If port 4040 is already
used by another agent, the launcher asks you to stop it or use the manual setup.

An existing `AWARDDESK_DEMO_PASSWORD` is reused instead of generating one. It must
be 12–256 bytes and should be separate from the admin password. Demo values are
process environment variables, not persistent database settings.

ngrok must be installed and authenticated first. On this PC it is installed at
`C:\ngrok\ngrok.exe`; other checkouts need it on PATH or at that location. Follow
the [official installation guide](https://ngrok.com/download/windows), then run:

```powershell
ngrok config add-authtoken YOUR_NGROK_AUTHTOKEN
```

On this PC, if `ngrok` is not on PATH, use
`& 'C:\ngrok\ngrok.exe' config add-authtoken YOUR_NGROK_AUTHTOKEN` instead.

Use your own ngrok token; do not put it into Git or share it with demo visitors.

## Manual setup: Windows, macOS or Linux

In one terminal, start the tunnel and leave it running:

```sh
ngrok http 8080 --inspect=false
```

Copy the HTTPS URL ngrok prints. In another terminal, start Awarddesk with that
exact URL and a separate demo password.

PowerShell:

```powershell
$env:AWARDDESK_PUBLIC_URL = 'https://your-assigned-host.ngrok-free.app'
$env:AWARDDESK_DEMO_PASSWORD = 'choose-a-long-separate-password'
go run .
```

macOS/Linux:

```sh
AWARDDESK_PUBLIC_URL='https://your-assigned-host.ngrok-free.app' \
AWARDDESK_DEMO_PASSWORD='choose-a-long-separate-password' \
go run .
```

Replace the example URL with the actual assigned URL, including a different ngrok
domain suffix if applicable. The app deliberately accepts one exact hostname,
not a wildcard. Update the environment and restart the app if the URL changes.
These startup variables are read from the process environment; editing an old
imported `.env` does not update them.

Do not rewrite Host to localhost or strip forwarding/origin headers. ngrok must
preserve the public Host and send `X-Forwarded-Proto: https`. The app rejects
forwarded requests pretending to be localhost. It also keeps its ordinary CSRF
token checks for public search submissions and cancellation.

The localhost URL remains usable without the demo password. Open
`http://127.0.0.1:8080/admin` on the host computer to manage accounts, sessions,
keys and settings. Visitors cannot use your local admin session through the
public URL. Admin links are hidden in the demo page.

## What visitors can do

Authenticated visitors can open the flight page, search airports, submit searches
and retrieve results. They share this workspace's accounts, queue, sessions and
cache. Demo authentication applies to the whole public site, including API and
asset requests. It is a shared demo, not a tenant-isolated hosted service.

Live searches and background session preparation retain their normal solver and
proxy costs. Test the page with cached results or wait for funded solver balances
before asking visitors to perform live searches. No live airline check is needed
to run the offline demo-access regression tests.

ngrok may show its own browser warning on some account plans. Its availability,
traffic limits and URL allocation depend on your ngrok account. The local launcher
disables agent HTTP inspection; any separate account-level traffic capture setting
is controlled in your ngrok dashboard.

## Local-only mode

For normal local use, stop the tunnel and clear the two variables, then run
`go run .` as usual. The launcher restores their previous environment values when
it exits. Without a configured public URL, public hostnames are rejected.

Implementation: [demo.go](../internal/web/demo.go) owns the demo access boundary;
[server.go](../internal/web/server.go) keeps origin and CSRF checks;
[demo_test.go](../tests/integration/demo_test.go) exercises them offline.
