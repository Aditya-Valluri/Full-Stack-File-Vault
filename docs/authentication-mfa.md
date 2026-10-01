# Authenticator MFA

MFA is optional for both USER and ADMIN accounts. Enforcing it for administrators
requires an enrollment/recovery rollout first; the current change does not lock
existing administrators out or change any production setting.

## Storage and configuration

Migration 000023 adds one enrollment per user and eight hashed recovery codes.
The seed is 20 random bytes, encrypted with AES-256-GCM, a fresh 12-byte nonce,
a format-version byte, and the user UUID as authenticated associated data.
Ciphertexts cannot be moved between accounts. Pending enrollment expires after
10 minutes. Only a valid six-digit authenticator code activates enrollment.
Email registration/reset codes remain seven digits.

Configure MFA_ENCRYPTION_KEY as base64 of 32 cryptographically random bytes.
The existing secret reader also supports MFA_ENCRYPTION_KEY_FILE; configure
exactly one source. Keep the key server-side, separate from the database, and
back it up securely. Never put it in source, examples, logs or browser code.
No key has been generated or installed for your local/live application by this
change. Browser tests generate an ephemeral key for their disposable backend.

Without a configured key, new enrollment is unavailable. Existing enrollments
still require a factor: TOTP fails closed if its key is missing/wrong. A valid
unused recovery code plus the password can still recover access. Do not replace
the key casually: existing seeds require the original key. Key rotation needs a
controlled re-encryption procedure; simply changing the environment value is
not rotation. Database restores must be paired with the matching protected key.

## Login and recovery

Login still uses the anonymous HttpOnly session, CSRF, password verification,
shared request budgets and progressive password backoff. The optional
secondFactor input contains a current authenticator code or an unused recovery
code. The password is verified again on the factor submission; no intermediate
authenticated session or separate pending-login token is issued. An authenticated
session is created only when both factors pass under the existing user lock.

TOTP uses RFC 6238 SHA-1 / six digits / 30-second steps, tolerates one adjacent
step and rejects any already-used step. Bad second factors count as rejected
login attempts. Missing second factor prompts only after a valid password.
Setup/enable/disable also use the existing identifier and source budgets.

Recovery codes have 128 random bits each. Only a domain-separated SHA-256 hash is
stored. They are consumed transactionally under the user lock, including under
concurrent requests. Enabling returns the codes once for saving in a password
manager; QR seeds and codes remain in component memory, not browser storage.
QR rendering is local, with no external QR service.

Setup and enable require the current password. Disable requires the current
password plus a valid TOTP/recovery code. Enable/disable increment auth_version,
revoke every session, and enter the existing security audit feed. Password
change/reset preserves enrollment and recovery codes and requires fresh login;
email access alone cannot bypass MFA.

The UI exposes setup in Account security, displays a QR and manual seed, then
asks for confirmation. Save recovery codes before returning to sign-in. If you
used a TOTP to sign in, wait for a new code before a second sensitive operation.

A rollback refuses to drop the MFA tables while active enrollments exist.
There is no silent administrative bypass or automatic account linking.

## Validation

Unit tests cover RFC reference vectors, replay rejection, nonce uniqueness,
ciphertext modification and cross-user ciphertext binding. Disposable database
tests cover reauthentication, expired setup, session revocation, TOTP replay,
concurrent recovery-code consumption and password-reset preservation. Browser
tests exercise QR setup, recovery, fresh login and disable.

References:
- [RFC 6238](https://www.rfc-editor.org/rfc/rfc6238)
- [OWASP MFA guidance](https://cheatsheetseries.owasp.org/cheatsheets/Multifactor_Authentication_Cheat_Sheet.html)
