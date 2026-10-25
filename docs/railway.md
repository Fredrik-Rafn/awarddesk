# Railway demo

Deploy one Go service from GitHub with a persistent volume. The Docker image
includes the frontend; no Node server, PostgreSQL service or browser is needed.
The public site and `/admin` require the demo password; Admin also requires the
workspace's existing admin password. Stored API keys remain write-only.

## Create the service

1. Create a Railway project and deploy `Fredrik-Rafn/awarddesk` from GitHub,
   branch `main`. Railway reads `Dockerfile` and `railway.json`. An initial deploy
   can fail until the domain and required variables below are configured.
2. Attach a persistent volume to this service, mounted at **`/data`**. A small
   volume is sufficient for this demo. Keep **one replica**; SQLite is shared
   state and the volume must belong to this service.
3. Under Settings → Networking, generate a public domain. Railway provides
   `RAILWAY_PUBLIC_DOMAIN`; the app uses `https://` plus that hostname. If you use
   a custom domain, set `AWARDDESK_PUBLIC_URL` to that exact HTTPS URL instead.
4. Set these service variables:

   | Variable | Value |
   | --- | --- |
   | `AWARDDESK_DB` | `/data/awarddesk.db` initially |
   | `AWARDDESK_DEMO_PASSWORD` | Your separate demo password, 12–256 bytes |
   | `GOMEMLIMIT` | `512MiB` |

   Do not copy the ngrok URL into `AWARDDESK_PUBLIC_URL`. Normally leave that
   variable unset and use Railway's generated domain. The Docker image already
   sets `AWARDDESK_LISTEN_HOST=0.0.0.0`; Railway supplies `PORT`. Startup refuses
   a public listener without a public URL and valid demo authentication.
5. Deploy the changes. The health check at `/healthz` returns only `ok`; it
   verifies application startup, not airline access or solver balances. Other
   routes retain authentication, hostname, HTTPS and CSRF checks.

Start with 1 GB RAM if the plan permits it. Disable Railway's optional app sleep
for an immediately available demo. Trial accounts need full outbound network
access for the proxy and Outlook IMAP connections; a limited trial can block them.

## Transfer the current workspace

The private export prepared on 25 October 2026 is:

```text
.state/railway/awarddesk-2026-10-25.db
```

It contains accounts, Outlook OAuth credentials, proxies, solver keys, saved
sessions, settings, cached results and search history. Its admin password is the
same as the source workspace. The export is ignored by Git and excluded from
Docker builds. Keep it private; uploading it to the Railway volume is sufficient.
No separate proxy file or credential import is necessary.

To make a newer snapshot, stop local searches and the local app, then run:

```sh
go run ./cmd/workspace-backup -out .state/railway/awarddesk-new.db
```

The command includes committed SQLite WAL data, checks backup integrity and
refuses to overwrite an existing destination. It does not modify workspace rows.
The default source is `data/awarddesk.db`; use `-source PATH` for another database.

Install the current [Railway CLI](https://docs.railway.com/guides/cli):

```sh
npm install -g @railway/cli@latest
railway login
railway link
railway service link
railway volume list
```

Run these from the local repository and select the intended project, environment,
service and volume. Upload under a **new filename**, while the initial deployment
still uses `/data/awarddesk.db`:

```sh
railway volume files upload .state/railway/awarddesk-2026-10-25.db /awarddesk-imported.db
railway volume files list /
```

The remote paths are relative to the volume root: `/awarddesk-imported.db` is
`/data/awarddesk-imported.db` inside the container. If there are multiple volumes,
pass `--volume NAME` before `upload`, for example:

```sh
railway volume files --volume data upload .state/railway/awarddesk-2026-10-25.db /awarddesk-imported.db
```

After the upload completes, change **`AWARDDESK_DB` to
`/data/awarddesk-imported.db`** in Railway and deploy that variable change.
The old deployment shuts down and the new deployment opens the imported database.
Never upload over the database currently used by a running server. Do not copy
`.db-wal`, `.db-shm` or lock files; the verified export is self-contained.

Leave the local app stopped after migration so both deployments do not reuse
the same airline sessions and accounts independently. Uploading a later snapshot
does not merge records; it replaces the workspace selected by `AWARDDESK_DB`.

## Verify and share

1. Open the generated HTTPS domain. Enter username `demo` and your
   `AWARDDESK_DEMO_PASSWORD` when the browser prompts.
2. Open `/admin` and sign in with the existing admin password. Confirm the account
   list, proxies and configured API-key indicators migrated.
3. In Settings, set search concurrency to **5** for this small demo. Existing
   SQLite settings take precedence over old import-only environment values.
4. Prepare a few accounts if their transferred sessions have expired, then run
   one real search. Check Outlook, proxy and solver access before sharing.
5. Share the HTTPS URL and demo login; include the separate admin password if the
   reviewer should manage the workspace. Keep solver/proxy balances available.

Automatic account upkeep pauses after one hour without searches and resumes on
the next search. Manual preparation still works. The volume survives ordinary
deploys and restarts; deleting the volume deletes the workspace. Railway's volume
backup availability depends on the plan, so retain the local export.

Offline checks and a Linux cross-build validate the deployment code. Actual
Railway deployment, container startup and upstream networking must still be
verified in your Railway account.

References: [persistent volumes](https://docs.railway.com/volumes),
[volume upload commands](https://docs.railway.com/cli/volume),
[health checks](https://docs.railway.com/deployments/healthchecks).
