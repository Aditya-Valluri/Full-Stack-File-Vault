# Password restrictions, share activity, and file details

## Password authentication

Migration 000020 extends the existing PostgreSQL authentication-budget table;
there is no process-local lockout store. Source and global admission limits remain
20 attempts per source per minute and 60 globally per minute. Password attempts
use a separate scope from the existing five-per-15-minute registration/recovery
identifier budget, so failed passwords do not consume the recovery allowance.

Four failed passwords receive generic credential errors. The fifth failure starts
a five-minute restriction. Repeated groups escalate to 15 minutes, 30 minutes,
then one hour, capped at one hour. Unknown, disabled and existing identifiers use
the same state machine. A successful login clears consecutive failures and
escalation; 24 hours without an admitted password attempt expires retained state.
No user disabled flag is changed. Blocked requests do not extend deadlines.

Database timestamps and short row locks enforce admission across replicas.
Outstanding verifications count toward the five-attempt admission ceiling.
Twenty-second leases recover reservations abandoned by crashes; verification
itself is bounded to ten seconds. Generation timestamps prevent an older result
from overwriting a newer success or recovery. Infrastructure errors do not count
as incorrect passwords. The frontend reports the server-provided retry duration,
rounded up to minutes; it does not enforce the restriction.

Password reset retains generic responses, MailSender, seven-digit expiring
browser-bound code hashes, single use, strong password validation, auth-version
increment, session revocation, and fresh login. An auth-version change atomically
clears password restrictions. Source/global abuse limits still apply to recovery.
MFA is not implemented; any future MFA design must retain its verification
requirements during recovery.

Temporary lockouts can still cause targeted temporary denial of service under
sustained attack. Expiry, a cap and recovery reduce that risk; they cannot promise
that attackers can never interrupt access. Legacy accounts without a verified
email retain their existing administrator-assisted recovery path.

## Sharing

Migration 000021 records authorized share inspection (OPENED) and admitted
download GET starts (DOWNLOAD_STARTED). A download event is counted once per
transport grant, and does not prove transfer completion. HEAD requests and merely
issuing a byte URL do not count as downloads. Repeated share-page requests can
produce repeated opens; these are not unique visitors.

Owners can read only activity for their logical files. Retain the latest 100
events per file, including revoked links; deleting a file deletes its history.
This is a bounded recent history, not a durable compliance audit or lifetime
per-share analytics. Download totals retain their existing file-level semantics.
An expired link's historical status is expired; otherwise a removed link is
revoked. Only events occurring after rollout are available.

Store event ID, logical file ID, share ID, access type, server timestamp,
share expiry and the authenticated recipient ID for a recipient-restricted link.
Public links remain anonymous to the owner, even when a visitor is signed in.
No IP, user agent, device, geography, raw share token, or account email is stored
in activity. Restricted recipients have their current username or verified email
resolved on read. Public visitors remain anonymous. The recipient ID is the same
account restriction the owner selected.

Anyone-with-link and specific-recipient modes use the existing high-entropy
tokens. Each creation produces an independent token. A specific recipient must
authenticate as the chosen account, supplied by its ID; no public email-account
directory is introduced. Link reuse cannot prove forwarding or identify an
anonymous person.

## File metadata and deliberate limits

File details expose existing owner-authorized metadata: logical file ID,
name, logical size, detected MIME, upload timestamp, private tags, preview
availability, and folder. A separate Share dialog displays active links and recent
share activity; Details does not render content or sharing controls. Deduplicated logical files
retain independent tags and share history. Account-level savings remain available.
Do not expose global deduplication hints, storage keys, credentials or raw tokens
through metadata. Creation of a share still returns its URL once, as required.

Client-declared MIME, original modification date, and scan results are
not persisted/supported, so the UI does not fabricate them. No format-specific
EXIF, GPS, PDF author or other embedded metadata is extracted or displayed.
Original downloads may contain embedded metadata; the application does not claim
to strip it or scan for malware.

Sharing supports download-only, preview-and-download, and view-only for supported
formats. View-only rejects download grants server-side, but cannot prevent copying
preview bytes or screenshots. Its optional visual watermark contains only the
project name and local access time. See [sharing](architecture/sharing.md) and
[authorized previews](architecture/previews.md) for the actual boundaries.

## Validation

Use the isolated PostgreSQL integration fixture and browser fixture; neither
sends Gmail messages or touches the live database. Tests cover concurrent login
admission, capped escalation, expiry, success reset, recovery during restriction,
session revocation, unknown identifiers, legacy login, private activity,
recipient attribution, anonymous labeling, download counting, revoked/expired
links and bounded history. Browser tests exercise the new controls and private
file details alongside existing cross-user and deduplication behavior.
