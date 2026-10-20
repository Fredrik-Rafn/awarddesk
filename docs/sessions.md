# ANA saved sessions

Run `go run .` from the project directory. The server starts one ANA session
preparation job automatically, then checks every five minutes while its last
result is successful. The frontend's **Prepare ANA session** button runs the
same manager manually. No flight-search endpoints are called.

## Reuse and renewal

1. Acquire an exclusive account/session file lock.
2. Load `.state/ana/<account-hash>.json` as readable JSON.
3. Restore cookies with original domain/path/expiry and deletion semantics,
   the authorization token, frontend API headers and browser identity.
4. Reserve the original proxy pool slot and use its exact locked session ID.
   Check the exit IP and verify the authenticated member with ANA.
5. If the file is absent, exceeds the one-day local reuse limit, or ANA confirms
   unauthenticated membership (HTTP 401 or no matching member), log in afresh
   and save the newly verified session. Credential/configuration identity
   changes also invalidate the old cache.
6. Save updated cookies after successful validation or use. Future searches
   call `Manager.WithSession`, receiving an exclusive session for their callback.
   Callbacks are not automatically replayed, even if a search fails.

The one-day cap is an application limit, not a claim about ANA's token lifetime.
Cookie expiry and server-side revocation can invalidate a session earlier.
Every use requires validation. HTTP 403, OTP, timeouts, changed/unavailable proxy
identity, and unreadable/corrupt files stop the job instead of triggering
repeated credential submissions. These errors pause background checks for that
server run; manual retry or restarting the server resumes preparation.

Existing good files are not overwritten on failed validation or OTP. An
expired file is removed before a fresh login; unsuccessful login is never saved.
All phases share a 150-second operation deadline. A saved file is not created
until the authenticated member number is verified.

## Proxies

Fresh login selects the next unused (not leased or quarantined) pool slot in
round-robin order, with a new random locked-session ID. It does not rotate
between requests. Restoring a session reserves its original slot and does not
generate a new ID. Removing/changing that slot causes restore to stop.

All 100 entries in the current `proxies.txt` specify `lifetime-1440`. The app
continues to use Evomi's stricter `lockedsession` mode, for which `lifetime`
does not apply. It fails if the peer goes offline instead of switching IPs.
Neither a one-day setting nor an unused pool slot guarantees a particular
residential peer stays online or that two different slots have distinct exits.

Reference: https://docs.evomi.com/proxy-instructions/residential-proxies/proxy-sessions/

## Storage and capacity

The JSON file contains authentication material and proxy credentials, but not
the account password (credentials remain in `.env`). There is no encryption or
separate key file. Files are git-ignored, bounded in size and replaced through a
flushed temporary file. macOS/Linux files are created with owner-only permissions.

An OS file lock prevents overlapping use by app processes sharing the same
account cache path: flock on macOS/Linux and LockFileEx on Windows. The OS releases
the lock on process exit; leftover `.lock` files are harmless. Different account
files have independent locks. The in-process manager rejects overlapping use
without building an unbounded queue. This is not a multi-host scheduler or an
account pool implementation. Files can be copied between machines, but expiry,
browser identity, credentials and the original sticky proxy still need to match.
See [portable file storage](storage.md).

The explicit CLI `go run ./cmd/ana-login` remains a fresh-login diagnostic and
does not share the server's session cache. Do not use it to benchmark reuse.
The future search integration should use the server's manager, not the CLI.

Offline tests cover JSON file round trips and replacement, process locks,
cookie scope/expiry/deletion, stored identity isolation, cache expiry, renewal,
no credential fallback on 403/transport failure, OTP failure without a cache,
exclusive proxy restoration and rotation, and maintenance stopping on errors.
Live persistence/reuse still requires a successful ANA login; earlier HTTP 403
and OTP results do not prove this feature works against ANA.

The automatic startup job was live-tested through the configured proxy pool on
October 20, 2026: ANA returned HTTP 403 at credentials after 19.54 seconds.
No session file was saved and the job entered failed state. No flight search
was attempted. Offline tests, vet, build and frontend syntax checks passed.
