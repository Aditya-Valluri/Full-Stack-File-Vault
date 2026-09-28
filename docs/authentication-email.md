# Authentication email delivery

## Inspected implementation and readiness

The existing GmailMailSender can send from fullstackfilevault@gmail.com once
that mailbox authorizes the sender OAuth client. No authentication architecture
changes are needed. Username and verified email/password login remain supported.
Google sign-in remains deferred. Password recovery behavior is documented below.

The API reads process environment variables, not a .env file automatically:

```dotenv
AUTH_MAIL_MODE=gmail
AUTH_MAIL_FROM=fullstackfilevault@gmail.com
GMAIL_SENDER_CLIENT_ID=
GMAIL_SENDER_CLIENT_SECRET=
GMAIL_SENDER_REFRESH_TOKEN=
```

The three GMAIL_SENDER variables also support corresponding _FILE variables.
Use either the value or its _FILE source, never both. File values are trimmed;
empty, unreadable or oversized files are rejected without printing their contents.
Store persistent credentials in a password manager/secret store or an
access-restricted file outside the repository. Never use VITE_ variables, source,
docs, tests, logs, command-line arguments or Git for real credentials.

The adapter uses a refresh-token grant at Google's fixed HTTPS token endpoint,
then POST /gmail/v1/users/me/messages/send. It does not implement initial sender
authorization, require an application callback, or use a service account.
The recommended initial authorization below uses a Web application OAuth client,
the server-side authorization-code flow, and offline access.

Only this scope is required:

```text
https://www.googleapis.com/auth/gmail.send
```

No Gmail read, modify, compose, Drive, Contacts, Calendar, profile or OpenID scopes
are needed. Setting a scope in oauth2.Config does not reduce a previously issued
broader grant. Create a dedicated sender client and authorize only this scope.
The adapter does not query mailbox identity: users/me means the mailbox that
authorized the refresh token. AUTH_MAIL_FROM sets the MIME From header; it is
not proof that a token belongs to that address.

## Google Cloud setup — manual

1. Use a separate browser profile signed in as fullstackfilevault@gmail.com.
   Do not authorize your personal Gmail. Open https://console.cloud.google.com/.
2. Create/select the project's dedicated Google Cloud project. Confirm the
   project selector before configuring APIs or credentials.
3. Open APIs & Services > Library, find Gmail API, and select Enable.
4. Open Google Auth Platform > Branding (older navigation calls this OAuth
   consent screen). Start configuration if necessary. Use an app name such as
   Full Stack File Vault Sender, and the dedicated account for support/contact
   details.
5. Under Audience choose External for a consumer gmail.com account. Keep Testing
   for this local setup and add fullstackfilevault@gmail.com under Test users.
   Recipients do not need to be OAuth test users; only the authorizing sender does.
6. Under Data Access > Add or remove scopes, add exactly
   https://www.googleapis.com/auth/gmail.send. Verify there are no other scopes.
   This is a sensitive send-only scope.
7. Under Clients > Create client, choose Web application. Name it
   Full Stack File Vault Gmail Sender. Do not select Desktop app, create an API
   key, or use service-account/domain-wide delegation instructions.
8. Add this exact Authorized redirect URI (no trailing slash):
   https://developers.google.com/oauthplayground
   Authorized JavaScript origins are unnecessary for this sender flow.
   No localhost or Render callback URI is needed.
9. Save the client ID and secret in your private secret manager. Do not download
   credentials into the repository or paste them into chat.

Google's Gmail Go quickstart uses a different desktop/read example; do not copy
its client type, scope or token.json storage pattern for this application.

## Refresh token setup — one-time operator authorization

1. In the dedicated sender browser profile open
   https://developers.google.com/oauthplayground/.
2. Open the configuration gear. Set OAuth flow to Server-side, endpoints to
   Google, Access type to Offline, and Force prompt to Consent Screen.
3. Check Use your own OAuth credentials. Enter the dedicated Web client ID and
   secret from the same project. Playground is Google's authorization helper:
   its UI displays credentials/tokens and its server proxies the exchange.
   Use it privately; do not screen-share, screenshot, record or export responses.
4. In Step 1 clear other selected scopes. Enter only
   https://www.googleapis.com/auth/gmail.send and click Authorize APIs.
5. Confirm the selected account is fullstackfilevault@gmail.com before consenting.
   Review that access is limited to sending email. If the account or permissions
   differ, cancel. Do not authorize your personal Gmail.
6. In Step 2 click Exchange authorization code for tokens once. Privately confirm
   the granted scope is exactly gmail.send. Save only the refresh token in your
   secret manager alongside this client's credentials. The app obtains its own
   short-lived access tokens; do not configure the displayed access token.
7. Disable Playground auto-refresh and close the private session when done.
   Do not generate/share a link containing credentials or tokens. Do not use
   Playground Step 3 to send an email; the single test below uses the application.
8. If no refresh token appears, first check Offline and own-client settings.
   If necessary revoke only this dedicated sender grant in the sender account's
   third-party connections, then repeat authorization. Revocation invalidates
   the previous sender token. Do not repeatedly refresh or retry blindly.

Own-client settings avoid Playground's default-client 24-hour revocation.
External apps in Testing with Gmail scopes receive refresh tokens that expire
after seven days. Record the authorization date and renew manually before expiry
when necessary. Publishing/verification is a separate Google process, not part
of this local task. Tokens can also be revoked; Gmail account send limits apply.

## Local environment — private session

Do not put real values in .env.example. In a fresh PowerShell terminal at the
repository root, use masked prompts to populate only this process environment:

```powershell
$env:AUTH_MAIL_MODE = 'gmail'
$env:AUTH_MAIL_FROM = 'fullstackfilevault@gmail.com'

function Set-PrivateEnvironmentValue([string]$Name) {
    if (Test-Path -LiteralPath ("Env:" + $Name + "_FILE")) {
        throw 'Use one secret source only; a file source is already configured.'
    }
    $secure = Read-Host $Name -AsSecureString
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try {
        [Environment]::SetEnvironmentVariable(
            $Name,
            [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer),
            'Process'
        )
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
        $secure.Dispose()
    }
}
Set-PrivateEnvironmentValue 'GMAIL_SENDER_CLIENT_ID'
Set-PrivateEnvironmentValue 'GMAIL_SENDER_CLIENT_SECRET'
Set-PrivateEnvironmentValue 'GMAIL_SENDER_REFRESH_TOKEN'
Remove-Item Function:\Set-PrivateEnvironmentValue

$env:PUBLIC_ORIGIN = 'http://127.0.0.1:5173'
$env:APP_ENV = 'development'
.\scripts\start-api.ps1
```

The existing local launcher applies migrations (including 000016 through 000018) and
starts the API using the inherited mail settings. It requires the existing local
PostgreSQL configuration and Docker Desktop. This is the local development
database, not Render. Do not dump the environment, enable HTTP tracing, run with
a debugger that records secrets, or redirect credential output into a file.
Masked input avoids putting credential values in typed command history; process
environment still contains secrets in memory. Close this terminal after testing.

In a SECOND fresh terminal, without the sender credentials:

```powershell
cd "D:\File Vault\apps\web"
npm.cmd run dev
```

Open http://127.0.0.1:5173, using the exact origin. A running Docker application
does not inherit these host environment changes; test this host API/Vite pairing.
The existing API settings and database credentials must not be replaced with
sender credentials.

## Exactly one local verification email

Choose the recipient address before this step. Use an inbox you control and,
for complete registration testing, one not already registered in the local vault.
No email has been sent by the setup or automated tests.

Pre-send confirmation:

- AUTH_MAIL_FROM is fullstackfilevault@gmail.com, and that same account authorized
  the token. The delivered From header must confirm it after sending.
- The reviewed sender logs no OAuth credentials, token responses, message body or
  verification code. Provider errors are replaced with fixed generic errors.
- The body contains only fixed verification instructions and a one-time code:
  no passwords, OAuth tokens, database details, files, internal URLs or attachments.
  The verification code itself is sensitive; keep it private.
- The sender has a ten-second total deadline, rejects redirects and makes no
  automatic send retry. The UI also does not automatically retry registration.

Process:

1. Open Create an account. Enter the chosen recipient.
2. Click Send verification code exactly ONCE. Do not click resend or use Playground
   to send another message.
3. A successful response means Gmail accepted the send request, not guaranteed
   inbox delivery. Check Inbox and Spam manually at the recipient.
4. Confirm the subject is Verify your Full Stack File Vault email and the From
   address is fullstackfilevault@gmail.com. Do not forward or share the code.
5. To finish the end-to-end authentication check, paste the code into the local
   form, choose/confirm a password, and activate the account. Then sign out and
   sign back in with the email/password. These actions send no further email.
6. Separately check an existing username account can still log in. No additional
   test email is needed.
7. Report only accepted/received/verification-completed or the generic failure.
   Never paste token responses, request headers, the code or passwords.

On failure, STOP after the first attempt. A timeout may occur after Gmail accepted
the message; check the recipient inbox and sender Sent folder manually before
considering any separately authorized retry. Inspect configuration privately:
correct client/token pair, Testing test-user membership, token age/revocation,
enabled Gmail API and account permissions. Do not enable provider-response
logging or add read scopes to troubleshoot.

## Render — documentation only

Eventually the existing full-stack-file-vault-web service needs exactly:

```dotenv
AUTH_MAIL_MODE=gmail
AUTH_MAIL_FROM=fullstackfilevault@gmail.com
GMAIL_SENDER_CLIENT_ID=
GMAIL_SENDER_CLIENT_SECRET=
GMAIL_SENDER_REFRESH_TOKEN=
```

Enter real credentials privately in service environment/secrets. The existing
launcher already forwards these only to the API, not the cleanup worker.
Never add them as frontend build variables. Do not change Render, trigger a sync,
deploy, rename resources, change plans or regenerate unrelated secrets for this
setup. No Google visitor-login settings are involved.

## Existing safeguards and automated coverage

The adapter validates recipient/header input, uses fixed plain-text templates,
bounds HTTP duration, serializes refreshes, caches access tokens only in memory,
sanitizes errors and avoids send retries. Unit tests use fake HTTP transports and
loopback SMTP; they do not contact Gmail or send real email.

Registration persists only hashed 15-minute codes, atomically creates an ordinary
10,000,000-byte-quota account and rotates sessions. Existing-email requests do not
disclose account existence or merge accounts. Origin/CSRF checks, shared admission
budgets, Argon2id passwords and existing authorization remain intact.
AUTH_MAIL_MODE=disabled disables registration delivery but preserves existing
username/email login. Development SMTP remains loopback-only and forbidden in
production.

## Official references

- [Gmail scope definitions](https://developers.google.com/workspace/gmail/api/auth/scopes)
- [Google OAuth Playground and its configuration](https://developers.google.com/oauthplayground/)
- [Google OAuth token expiration](https://developers.google.com/identity/protocols/oauth2#expiration)
- [Google Auth Platform navigation](https://developers.google.com/workspace/gmail/api/quickstart/go)
- [Gmail message sending](https://developers.google.com/workspace/gmail/api/guides/sending)

## Password policy and seven-digit verification

New passwords require at least 15 non-padding Unicode characters and at most
1024 UTF-8 bytes. The backend rejects entries in the vendored SecLists common-
password list, project-specific obvious passwords, simple numeric/punctuation
decorations and substitutions, repeating short patterns, and obvious sequences.
Use a unique passphrase of unrelated words or a password manager. Uppercase,
digits and symbols are not mandatory. Password bytes are not normalized before
hashing. The local list is not exhaustive and cannot guarantee that every weak
or breached password is detected; no user password is sent to an external checker.
Existing stored passwords continue to verify; this change does not reset accounts.

Migration 000018 adds browser binding and a persistent five-attempt counter.
Verification emails now contain exactly seven ASCII digits (leading zeroes are
valid). Codes are generated with cryptographic rejection sampling and stored as
HMAC-SHA256 digests keyed by the high-entropy HttpOnly anonymous session token.
The same code in a different browser/session cannot activate the account.
Session cookies retain their original 256-bit entropy; reset codes use a separate purpose-bound digest.

Each challenge admits at most five verification attempts, including invalid
formats and unsuccessful password-policy submissions. Attempts commit before
verification and survive rollback; peer/global admission limits still apply.
The runtime role cannot update counters or challenge fields directly: a restricted
database function only increments an unexpired challenge below its attempt cap.
A newly requested code replaces the prior challenge for that browser.

Codes expire after 15 minutes at most and also require the original anonymous
session, whose lifetime is ten minutes from bootstrap. Complete verification in
the same browser promptly. If that session expires, start registration again and
request a new code; bootstrap cannot transfer the old challenge to a new session.
Pending old-format codes are invalid after upgrading. Existing accounts remain
unchanged. Restart the local API through scripts/start-api.ps1 to apply migration
000018 and load the code; do not request real test email until restart is complete.

## Password reset and change (migration 000019)

Recovery is implemented locally and validated with isolated PostgreSQL integration
and browser tests. Real Gmail recovery delivery has not been tested.

Verified email/password accounts can use Forgot password. The request sends a
seven-digit code without revealing whether the email belongs to an eligible
account. Ineligible addresses receive the same message, but their challenges
cannot change a password or create an account. Delivery is subject to existing
authentication budgets and the configured mail sender.

Reset codes are cryptographically generated, purpose-separated from registration
codes, and bound to the requesting anonymous browser session. They expire after
15 minutes or when that session expires, whichever comes first. Five attempted
submissions exhaust a challenge, including rejected new passwords. Requesting
a replacement invalidates the prior code for that browser.

Authenticated users can change their password under Account security by supplying
the current password. This works for legacy username credentials and verified
email/password identities. Username-only accounts cannot use email reset.

Both operations enforce the new-password policy and atomically revoke existing
sessions and outstanding reset challenges. A fresh login is required. Reset does
not automatically sign in or link accounts. Database functions bind updates to
the session and credential version; the runtime role has no direct access to the
reset challenge table or permission to update credential hashes.

Restart the local API with scripts/start-api.ps1 in the terminal holding the
mail configuration to apply migration 000019. Automated tests use captured local
mail only. Request approval before sending any real recovery email. Never share
codes, passwords, client secrets, or refresh tokens in logs or screenshots.

## Sign-in help and administrator contact

The sign-in screen separates account creation, password recovery, help instructions,
and administrator contact. Contact submits one GraphQL mutation using an anonymous
session and CSRF protection. It uses the existing configured mail sender and Gmail
send-only scope; no new OAuth permission or database migration is required.

The recipient is fixed server-side to fullstackfilevault@gmail.com. Visitors cannot
choose a recipient or mail headers. Subject (120 UTF-8 bytes) and message (4000
UTF-8 bytes) are validated and encoded as plain text in the email body. The mail
subject is fixed. Visitor identity is unverified; a reply address can be included
in the message, but must not be treated as proof of ownership.

Support delivery shares existing authentication budgets and uses a fixed support
identifier, limiting support to five messages across the application per 15 minutes.
This conservative cap can temporarily block legitimate contact during abuse. No
automatic retries, attachments, credentials, or message contents in application logs.
There is no durable ticket queue: success means the mail provider accepted the
request, not confirmed inbox delivery. When delivery is disabled the dialog explains
that it is unavailable. Tests use local captured mail, never real Gmail delivery.
