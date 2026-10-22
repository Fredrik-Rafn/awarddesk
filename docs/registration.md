# EVA bulk registration

Open **Admin → Accounts & sessions → Create EVA accounts**. Paste up to 500
Outlook mailboxes, one per line:

```text
email:password:refresh_token:client_id
```

Review EVA's membership terms and privacy policy, confirm the checkbox, and
submit. Mailbox passwords are discarded; OAuth2 IMAP performs read-only email
access. Generated EVA passwords and profiles are stored privately in SQLite.
Existing local accounts/imports are skipped without changing their credentials.
The example mailbox from the HAR already exists at EVA and is not a fresh signup.

Each worker obtains an exclusive proxy-pool slot with a fresh random locked
sticky identity. Registration, search and login preparation share the configured
worker ceiling (default four, maximum sixteen). Cookies and transports stay
isolated. Provider-assigned physical IPs are outside the app's control.

The module follows the recorded English/UK registration flow: profile details,
registration reCAPTCHA (and image fallback if required), membership agreement,
verification email link and alphanumeric code, then activation of email login.
Names, an adult birth date, phone number and strong password are generated.
Optional marketing/data-sharing choices are declined.

Only fresh messages for the exact recipient, EVA sender and registration subject
are accepted. Inbox UIDs are checkpointed before submission. Junk is also checked
with strict request-time and one-hour expiry validation. Verification links must
be HTTPS on EVA's exact registration portal; tracking links are not followed.
Login OTPs retain their separate three-minute expiry and subject validation.

Successful registrations enter the enabled account pool and prepare the standard
saved EVA member session. The account editor loads the stored password and OAuth
token, initially masked with eye buttons. The creation panel shows a temporary
table of queued/running work submitted from the current page. Completed rows
disappear. Refresh, sign-out or switching admin sections clears this view;
background work and durable recovery checkpoints continue. Unresolved setups
appear as Needs attention in the accounts list, including unconfirmed memberships.

Registration checkpoints are saved before mutations. Network failures before
creation can safely start fresh setup. The final submission must return EVA's
registration-complete container before it is acknowledged and waits for email;
rejections and unknown responses retain their original error and checkpoint.
**Resume verification** checks the saved submission without submitting it again.
After the original one-hour verification window expires, an unverified attempt
offers **Restart signup** following an explicit inspection confirmation. This
atomically archives the full prior attempt and queues a fresh flow. Known member
numbers and uncertain verification submissions cannot be reset this way.
Uncertain OTP/email-login submissions require inspection rather than
blind replay. If creation succeeded but login preparation failed, **Retry setup**
prepares the existing account without creating it again.

Safe initial login-preparation failures receive one additional bounded attempt;
uncertain credential/OTP submissions are not replayed. If the normal background
session job later recovers the account, onboarding updates to Ready automatically.

Server shutdown/restart resumes queued or safe email-polling checkpoints. No
payment, ticketing or flight search is triggered by onboarding itself.

Code entry points: [mailboxes.go](../internal/registration/mailboxes.go) validates
and deduplicates input; [service.go](../internal/registration/service.go) owns
checkpoints and recovery; [workers.go](../internal/registration/workers.go) claims
work once and waits for notifications when idle. Imports and retries wake workers
immediately. The 15-second upkeep tick provides a fallback after database errors.
