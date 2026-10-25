# Fly.io demo

The checked-in `fly.toml` configures one shared CPU, 1 GB RAM, an always-running
HTTP service and a persistent volume at `/data`. Fly handles HTTPS. The same
Docker image serves the frontend and Go backend. No separate database service,
dedicated IPv4 address or second machine is needed.

The CLI commands below use `fly`; `flyctl` is equivalent. On the original Windows
PC the installed binary is `C:\Users\bobra\.fly\bin\flyctl.exe`.

## First deployment

Install the [Fly CLI](https://fly.io/docs/flyctl/install/), create an account,
add a payment method and run `fly auth login`. The free trial is too short for
an unattended multi-day demo.

From the repository root, choose a globally unique app name:

```powershell
$app = 'your-unique-awarddesk-demo'
fly apps create $app --org personal
fly volumes create awarddesk_data --region ams --size 1 --app $app --yes
fly ips allocate-v4 --shared --app $app
fly ips allocate-v6 --app $app
```

Store a separate demo password without putting it in Git or command-line history:

```powershell
$demoPassword = [Guid]::NewGuid().ToString('N')
"AWARDDESK_DEMO_PASSWORD=$demoPassword" | fly secrets import --stage --app $app
fly deploy --app $app --remote-only --ha=false --strategy immediate
```

Keep `$demoPassword` in your password manager. The app derives its exact public
origin from `FLY_APP_NAME`, so its URL is `https://APP_NAME.fly.dev`. Custom domains
can instead use the `AWARDDESK_PUBLIC_URL` secret. The health endpoint `/healthz`
returns only `ok`; other pages remain authenticated. Admin at `/admin` requires
the demo login plus the workspace's separate admin password.

Always pass `--ha=false` on deployment and verify that only one machine exists.
The configuration deliberately omits an app name so each operator must select
the correct app with `--app`.

## Move the existing SQLite workspace

Leave the original local app stopped once migrating. Make a new verified export:

```powershell
go run ./cmd/workspace-backup -out .state/fly/workspace.db
```

The backup includes accounts, Outlook OAuth credentials, solver keys, proxies,
saved sessions, settings, cache and history. Do not commit it or copy live WAL,
SHM or lock files. The backup command includes committed WAL data and checks
integrity. It refuses to overwrite an existing destination.

Upload under a different filename while the running bootstrap app uses
`/data/awarddesk.db`:

```powershell
fly ssh sftp put .state/fly/workspace.db /data/awarddesk-imported.db --mode 0600 --app $app
fly secrets set AWARDDESK_DB=/data/awarddesk-imported.db --app $app
```

The variable change restarts the service on the imported database. Fly secrets
override the bootstrap path in `fly.toml`, including on later deployments. Never
upload over a database currently in use. On subsequent migrations, choose another
new remote filename and switch the database path only after the upload completes.
Do not run the original local server against the same accounts concurrently.

## Check and share

```powershell
fly status --app $app
fly checks list --app $app
fly machines list --app $app
fly volumes list --app $app
```

Open the HTTPS URL, use username `demo` and your generated password, then sign
into `/admin` with the existing workspace password. Confirm the migrated accounts,
proxies and API-key configured indicators. Saved API keys are never returned to
the browser; account credentials remain available to authenticated administrators.
Set search concurrency to five for the demo, prepare a few accounts if sessions
have expired, and run one live search before sharing the URL and credentials.

Account upkeep pauses after one hour without searches and resumes on the next
search. Manual preparation remains available. One machine with an attached volume
has brief downtime during deployment; this is a small shared demo, not a highly
available service.

## Cost and cleanup

The intended 3–5-day deployment should cost only a few dollars for the machine
and small volume at current rates. Region, traffic, remote builds, taxes and any
additional resources can change the bill; proxy and solver usage are separate.
There is no automatic $30 spending cap configured. Monitor the Fly billing page,
keep one machine, and avoid adding paid extensions or dedicated IPv4 addresses.

The service stays running until you stop or delete it; `fly.toml` does not schedule
automatic deletion after five days. When the review is finished, deleting this
dedicated app removes its machines and persistent data:

```powershell
fly apps destroy $app
```

Retain a private backup if the hosted data is still needed. Merely closing your
terminal does not stop the hosted app. A stopped machine can still incur storage
charges; deleting unused resources is the cleanest way to end demo hosting costs.

References: [configuration](https://fly.io/docs/reference/configuration/),
[database upload](https://fly.io/docs/flyctl/ssh-sftp-put/),
[pricing](https://fly.io/pricing/).
