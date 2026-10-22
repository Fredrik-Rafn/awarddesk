# SQLite workspace storage

The running application stores configuration, proxy lists, EVA accounts, scoped
cookie/token snapshots, rotated Outlook refresh tokens, complete flight-result
caches, permanent search/registration history and admin sessions in `data/awarddesk.db`.

The pure-Go SQLite driver works on Windows/macOS/Linux without CGo. WAL mode,
a five-second busy timeout and a bounded connection pool allow readers during
short writes. HTTP/solver requests never hold an SQL transaction. OS locks protect
individual account/session operations across local processes and release on
crashes; lock files contain no credentials. One server owns each workspace.

## Migration

The first boot imports the existing `.env`, configured proxy file and valid JSON
state under `.state` in a transaction. Imported JSON keys retain their account
hashes and normalize path separators for cross-platform use. The first EVA account
is created from the old credentials. Caching is set to 30 minutes. A migration
marker prevents later startups from overwriting Admin changes with stale files.
Old files remain as backups, and are no longer read by the server or diagnostics.

A fresh checkout needs only `go run .`: sign into `/admin` using `admin1234`,
then add accounts, proxies and solver keys. The password is stored as a bcrypt
hash. Admin cookie tokens are hashed in SQLite, expire after 12 hours, and can be
revoked by signing out or changing the admin password.

## State and permanent history

The `states` table stores portable JSON blobs, keyed separately for each EVA
account, Outlook mailbox/client ID, and account/query cache combination. Browser
identity, scoped cookies and the exact sticky proxy travel together. Flight
results contain parsed display data, without traveller profiles or booking tokens.
Only complete successes are cached; expiry is checked on every read. Maintenance
prunes expired caches and bounds the cache at 1,000 entries. Search history, its
full results and statistics are retained permanently, with all-time selected by
default. The former retention setting is removed and ignored. Records pruned
before this upgrade cannot be restored.
Interrupted jobs are recorded rather than replayed on restart.

Admin provides a flight-cache switch (`FLIGHT_CACHE_ENABLED`). Disabling it
requires a custom confirmation dialog and atomically clears every cached flight.
Cache reads and writes stop immediately, including writes from running searches.
Historical results and saved login sessions remain available. Re-enabling caching
resumes normal caching; a legacy zero lifetime resets to 30m.

Admin history is paginated on the server, with separate compact summaries and
authenticated full-result inspection. Analytics cover the entire selected time
window, rather than only the visible history page. CSV export reads one database
snapshot and excludes account/session secrets and full result payloads.

The `settings` table holds global runtime configuration and the proxy list;
`accounts` holds individual EVA and Outlook credentials; `runtime` records
per-account status, consecutive-failure counters, retry deadlines and attention flags.
Configuration edits apply
when an EVA account becomes idle. Rotated mailbox tokens take precedence over the
original refresh token; explicitly replacing an account's OAuth configuration
invalidates its old saved mailbox token.

## Back up or move

Stop Awarddesk with Ctrl+C before copying `data/awarddesk.db`. SQLite may have
uncheckpointed writes in `-wal` while running; copying only the main file then is
not a safe backup. Copy a cleanly stopped database to the new checkout to retain
configuration, accounts, sessions and caches. Lock files need not be copied.

The database contains plaintext account/API/proxy secrets and portable tokens.
It remains excluded from Git. Local directories/files request 0700/0600 permissions
on Unix. There is no DPAPI, Keychain binding or machine-specific encryption key.
A copied live session is useful only while its provider authentication and original
sticky proxy/IP remain valid. Separate computers do not share local locks.
