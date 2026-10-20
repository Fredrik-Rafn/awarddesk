# Portable session files

The app reads and writes ordinary JSON files on macOS, Windows and Linux.
There is no DPAPI, Keychain integration, encryption key or database requirement.

```text
.env                              # Account credentials and solver configuration
proxies.txt                       # Proxy pool
.state/
  eva/<account-hash>.json          # EVA cookies, account, expiry and exact proxy
  ana/<account-hash>.json          # ANA tokens, browser state and exact proxy
  outlook/<account-hash>.json      # Email, client_id, refresh_token, updated_at
```

The account hash is deterministic. EVA uses the email, ANA the member number,
and Outlook the email plus OAuth client ID. Each account therefore has a separate
file; updating one does not overwrite another. EVA's JSON also identifies its
account in the `Account` field.

Cookies retain their domain, path, expiry and other attributes. The app loads the
file, restores the browser state and verifies the logged-in session before reuse.
The original sticky proxy stays attached to the session. Missing or expired
sessions follow the existing bounded login/renewal flow.

Outlook's newest refresh token is saved in its JSON file and takes precedence
over the original `.env` token on subsequent refreshes. The token file is checked
against the configured email and client ID.

Files are readable plaintext and remain excluded from Git. Writes use a flushed
temporary file before replacement. On macOS/Linux directories are created with
0700 permissions and files with 0600. Per-account OS locks prevent concurrent
writes and are released when a process exits.

You can copy `.state` between computers without conversion. A copied airline
session is usable only while its original sticky proxy and provider session
remain valid; the app validates them instead of assuming portability preserves
authentication. A file lock coordinates local processes, not separate computers.

The current `.env` selects one active account for each provider. The storage
already keeps accounts separate for a future session pool; automatic selection
and scheduling across multiple accounts/sessions are not implemented yet.

Previous Windows-encrypted `.session` and `.token` files are not loaded by the new
format. Start fresh on the laptop using `.env`, or copy the migrated `.json` files
from this machine. Do not rename encrypted legacy files to `.json`.
