# 07: Security

## Secrets & encryption

- Connection credentials, channel secrets, AI keys and secret settings are encrypted with
  **AES-256-GCM**. Ciphertext format: `v1:<key_id>:<nonce_b64>:<ct_b64>`.
- Master key from `ROWBIRD_MASTER_KEY` (base64, 32 bytes) or `ROWBIRD_MASTER_KEY_FILE` (Docker
  secrets). Never stored in the DB.
- **Rotation:** `rowbird keys rotate` re-encrypts every value in one transaction, keeping its
  associated data (08, "Master key rotation"). `ROWBIRD_MASTER_KEY_PREVIOUS` (or `_FILE`) keeps an
  older key for decryption only while several instances switch keys.
- Secrets never appear in logs (redacting `slog` handler), errors, API responses, exports or
  notifications. Tests MUST assert this for every secret-handling feature.
- The heartbeat URL (it often carries a token) is a secret setting: encrypted with the master key,
  never returned (`heartbeat_url_configured`), and heartbeat failures are logged by error code only.
  `ROWBIRD_METRICS_TOKEN` protects `/metrics` when set.
- Plugin configurations with secrets (connections, channels) go through `internal/secretconfig`
  (ADR-0022): fields marked `x-secret` are split out, encrypted together with
  `<kind>:<id>:secrets` as associated data, returned as `{ "configured": true }`, and removed (with
  URL credentials) from driver and destination error messages before those are logged or stored on
  attempts and channel health.

## Authentication

- Passwords: argon2id (memory 64 MiB, time 3, parallelism 2; tune in an ADR if needed), min length 10,
  checked against a small common-password list.
- 2FA: TOTP (RFC 6238, SHA-1, 6 digits, 30 s, one step of clock skew each way) + 10 one-time
  recovery codes (SHA-256 hashes). A TOTP time step is accepted only once (replay protection). The
  TOTP secret is encrypted with the master key. Admin can **require 2FA**.
- Passkeys (WebAuthn, ADR-0027) as primary or second factor, and either way they count as 2FA
  for the workspace requirement. The relying party is the host of `ROWBIRD_BASE_URL` (passkeys are
  unavailable without it). Passwordless sign-in uses discoverable credentials and requires user
  verification (PIN or biometrics); the second-factor step prefers it. Registration requires the
  current password, like other 2FA changes. Each ceremony is stored server side, single use, 5
  minutes. A signature counter that goes backwards (a possible clone) refuses the sign-in, and
  failures count toward the lockout. An admin's 2FA reset removes the user's passkeys too.
- Sessions: 7-day sliding expiry (configurable), list + revoke, "sign out everywhere", rotate token
  on login and privilege change.
- Progressive lockout on failed logins: per account (stored, so it holds across instances) after 5
  failures, 1 minute doubling on each further failure up to 15 minutes; failed second-factor codes
  count too. Per client IP, an in-memory token bucket of 30 attempts per 10 minutes on login,
  second factor and setup. Unknown accounts, wrong passwords and disabled accounts get the same
  answer and take the same time (a dummy hash is computed).
- The client IP honors `X-Forwarded-For` only from `ROWBIRD_TRUSTED_PROXIES`, read from the right
  and skipping trusted hops.
- "Forgot password" sends a one-use link by email through the system mailer: a 256-bit `rbr_`
  token stored as a hash, valid 30 minutes, replaced by a new request, at most one email every 5
  minutes per account. The request answers the same whether or not the account exists, and the
  email is sent in the background so the timing does not tell either. Using the link unlocks the
  account and signs it out everywhere; the second factor is still asked at the next sign-in.
- Passwords and 2FA changes by the user require the current password. Changing the password signs
  out the other sessions; role changes, disabling and admin resets sign the user out everywhere.
- OIDC (ADR-0027): authorization code with PKCE (S256), state and nonce checked in constant time,
  carried in a 10-minute cookie encrypted with the master key (so any instance can finish the
  flow); the ID token is verified (issuer, audience, signature, expiry, nonce). The user is found by
  `(issuer, subject)`; else an existing user with the same email is linked only when the provider
  says `email_verified` and the user is not linked to another identity; else, with
  auto-provisioning, a user is created with the default role (viewer or editor, never admin) and no
  password. A non-empty domain allowlist admits only verified emails in those domains. Disabled users
  are refused. OIDC sessions are not asked for Rowbird's second factor: the provider owns the
  password and 2FA. The client secret is a secret setting. Requests to the provider go through the
  network policy.

## Authorization

- Deny by default; each operation declares its required role/scope.
- `workspace_id` filtering is enforced in the **store layer** (repositories take a scoped context);
  handlers cannot bypass it (prevents IDOR).
- Permission tests for every endpoint (table-driven: role × endpoint → expected status).

## Protecting user databases

- Read-only transactions where supported; server-side timeouts; row limits; single statement by
  default (`allow_multi_statement` per connection, off by default, UI explains the risk).
- Parameters always bound. Connection UI warns when the DB user can write.

## Generated content

- HTML outputs (email, Telegram, preview) escape all database-originated values; links in messages
  are only absolute http(s) URLs.
- Message templates are sandboxed (04): no partials, escaping cannot be turned off, only documented
  variables, at most 10,000 characters, values escaped for each destination. The delivery preview
  shows HTML in an iframe with an empty `sandbox` (no scripts, no same origin).
- CSV/XLSX: text cells starting with `=`, `+`, `-`, `@`, tab or CR are neutralized (prefixed with `'`).
  Numbers stay numbers. Column names get the same treatment.
  Typed numeric cells (e.g. negative numbers in XLSX) are written as numbers and are not affected.

## Network (SSRF)

`ROWBIRD_NETWORK_POLICY`: `open` (default for self-hosted) or `block-private` (blocks RFC1918,
loopback, link-local, cloud metadata `169.254.169.254`, IPv6 equivalents). Enforced in a shared
dialer used by connectors, destinations (HTTP clients and SMTP) and AI providers (the S3 storage
backend is configured by the operator and dials directly), resolving DNS once and checking the
resolved IP (anti DNS-rebinding). Optional allowlist of CIDRs/hosts. With an SSH tunnel the policy
applies to the SSH host; the database behind it is resolved and reached by the SSH host.

## User database connections

- SSH host keys are pinned by SHA256 fingerprint: the first test stops and shows the fingerprint for
  an admin to confirm; a different key later is refused (`connection.ssh_host_key_mismatch`).
- Driver error messages are logged only after removing the connection's secret values and URL
  credentials; API responses carry the error code, never the driver message.
- SQLite connections are limited to `ROWBIRD_SQLITE_DIRS`, opened read-only, and can never point at
  Rowbird's own store.

## Shared links

256-bit random tokens (`rbl_` + 43 base64url characters), stored only as a SHA-256 hash and looked
up by hash (so no token comparison happens in memory), expiry (1 hour to 90 days, default 7 days),
optional login requirement (a session in the link's workspace; otherwise a redirect to the login
page and back), revocation, `X-Robots-Tag: noindex, nofollow`, `Referrer-Policy: no-referrer`,
`Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, downloads as attachments, per-IP rate
limit (60 per minute), download log (time, user when signed in, IP, user agent). Errors are short
pages in the visitor's language that say nothing about the file. With S3 storage the download
redirects to a presigned URL valid for at most 5 minutes. The token cannot be shown again after the
message; a resend creates new links and the earlier ones stay valid until they expire or are
revoked (ADR-0023).

## Outgoing webhooks

HMAC-SHA256 over `timestamp.body` with header `X-Rowbird-Signature: t=...,v1=...`; docs show how to
verify and reject stale timestamps (> 5 min). The HMAC secret and secret headers are channel
secrets; configured headers cannot override `X-Rowbird-*`, `Content-Type`, `Content-Length` or
`Host`.

## AI

Off until an admin chooses a provider; until then nothing is sent to any AI service. Only the
request, the dialect, the current SQL and the schema are sent: excluded tables are removed from the
schema entirely, and rows, samples and results never are. The instructions tell the model that the
request, the SQL and the schema are data, not instructions. The output is a proposal validated by
the server; nothing executes without user action, and applied SQL still runs read-only. The
provider's key is encrypted like other secrets and never returned; provider errors are scrubbed of
it. Requests go through the network policy and are limited per user. Logs keep the provider, model,
duration and token counts, never the prompt, the schema or the key.

## Web hardening

Every response carries `Content-Security-Policy` (`default-src 'self'`, scripts only from the same
origin, inline styles allowed because the component library sets style attributes, `object-src
'none'`, `frame-ancestors 'none'`, `form-action 'self'`), `X-Frame-Options: DENY`,
`X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`,
`Cross-Origin-Opener-Policy: same-origin` and a `Permissions-Policy` that turns off camera,
microphone and geolocation. `Strict-Transport-Security` (two years, subdomains) is sent when the
public base URL is https or the request arrived over TLS, directly or through a trusted proxy's
`X-Forwarded-Proto` (the nearest proxy's value). No response has `Access-Control-*` headers and CORS
preflights answer 403. Cookies are `HttpOnly` (except the CSRF cookie the SPA echoes), `SameSite=Lax`
and `Secure` with an https base URL; state-changing requests need the CSRF header and JSON.

## Security events (OSS)

Logins (success/failure, with the method), 2FA changes, passkey changes, OIDC links and provisioned
users, API key create/revoke, user/role changes, connection/channel create/update/delete, settings
changes, import applied, master key rotations. Viewable by admins; kept 365 days.

## Project security

`govulncheck` + Dependabot in CI, signed releases (cosign), SBOM, `SECURITY.md` with
`security@rowbird.dev` and a disclosure policy.

## Checklist

Each rule above and the test that holds it. Go tests are named `package.Test` (`api` is
`internal/api`); E2E steps are in `web/e2e`.

| Area | Rule | Tests |
|---|---|---|
| Secrets | AES-256-GCM, fresh nonces, tampering and wrong associated data detected | `crypto.TestRoundTrip`, `TestNonceIsFreshEveryTime`, `TestTamperingIsDetected` |
| Secrets | Master key from value, file or generated; errors without key material | `crypto.TestLoadFromValue`, `TestGenerateThenReuse`, `TestInvalidKeysDoNotLeak` |
| Secrets | Rotation of every value in one transaction; previous key accepted; refused while old instances run | `keys.TestRotate`, `TestRotateWithThePreviousKey`, `TestRotateRollsBackOnFailure`, `TestRotateRefusesWhileOldInstancesRun`; `cli.TestKeysRotateGeneratedKey`, `TestKeysRotateConfiguredKeyNeedsTheNewKey` |
| Secrets | Never in responses, logs or errors | `auth.TestSecretsNeverLeak`, `secretconfig.TestScrub`, `connections.TestSecrets`, `connections.TestSafeMessage`, `api.TestOIDCOverHTTP` |
| Secrets | Heartbeat URL encrypted and write-only; metrics token | `notify.TestAlertSettings`, `api.TestMetricsRoute` |
| Authentication | argon2id, length, common passwords, rehash | `auth.TestHashAndVerify`, `TestValidatePassword`, `TestRehashWhenParametersChange` |
| Authentication | TOTP with replay protection, recovery codes, limited challenges | `auth.TestTOTPLogin`, `TestTOTPManagement`, `TestChallengeAttemptsAreLimited`; `api.TestTwoFactorLoginOverHTTP`; E2E "signing in asks for the second factor" |
| Authentication | Passkeys: registration with the password, passwordless and second factor, user verification, clone detection, single-use ceremonies, admin reset | `auth.TestPasskeys`, `TestPasskeyFailures`; `api.TestPasskeysOverHTTP`; E2E "a passkey added on the profile signs in without a password" |
| Authentication | Lockout per account, rate limit per IP, same answer for unknown accounts | `auth.TestLoginAndLockout`, `TestLockoutDurationGrows`; `api.TestLoginErrorsAndLockout`, `TestLoginRateLimitPerIP`, `TestRateLimiter`; E2E "repeated wrong passwords lock the account" |
| Authentication | Forgot password: same answer, one use, expiry, throttle, sign-out everywhere | `auth.TestPasswordReset`; `api.TestPasswordResetOverHTTP`; E2E "a forgotten password is reset through an emailed link" |
| Authentication | Sessions: sliding expiry, revoke, sign out everywhere, restrictions | `auth.TestSessions`, `TestRestrictions`; `api.TestRestrictedSessionAndAPIKeys` |
| Authentication | Client IP and HTTPS only from trusted proxies | `api.TestClientIP`, `TestHSTS` |
| Authentication | OIDC: PKCE, state, nonce, verified-email linking, provisioning, domain allowlist, disabled users, no local 2FA | `auth.TestOIDC`, `TestOIDCProvisioning`, `TestOIDCSettingsValidation`; `api.TestOIDCOverHTTP`; E2E "the admin sets up single sign-on and signs in through the provider" |
| Authorization | Deny by default, every operation declared, role x scope matrix | `api.TestPermissionMatrix`; `api` (root) `TestOperationConventions` |
| Authorization | Workspace scoping in the store | `store.TestScopedRequiresWorkspace`, `TestScopedIsolation` |
| User databases | Read-only, timeouts, row limits, single statement, bound parameters | connector conformance suites (`internal/plugin/connectortest`) for every driver |
| User databases | SSH host keys pinned; SQLite limited to its directories | `sshtunnel.TestPasswordTunnelAndHostKeyConfirmation`, `sqlite.TestAllowedPath`, `connections.TestCreateSQLite` |
| Generated content | Escaping and formula neutralization | formatter conformance suites (`internal/plugin/formattest`); `msgtemplate.TestRender`, `TestValidate` |
| Network | `block-private` policy with DNS resolved once | `netx.TestIsBlocked`, `TestDialContext` |
| Shared links | Hashed tokens, headers, expiry, revocation, rate limit | `api.TestPublicLinkPage` |
| Webhooks | HMAC signature, protected headers | `webhook.TestPayloadAndSignature`, `TestParseHeaders` |
| AI | Excluded tables removed, output validated, never executed | `ai.TestRenderSchemaBudgetAndRelevance`, `TestReadOnly`, `TestProposalWarningsAndFailures` |
| Web hardening | Headers, HSTS, no CORS, CSRF, JSON only, cookies | `api.TestSecurityHeaders`, `TestHSTS`, `TestCORSPreflightIsRefused`, `TestCSRF`, `TestSetupOverHTTP` (415), `TestSecureCookies` |
| Operations | Retention on one instance, backups, restore safety | `maintenance.TestRetention`, `TestLeaseIsExclusive`; `backup.TestWriteAndRestore`, `TestRestoreRefuses`, `TestScheduler`; `cli.TestBackupAndRestore`; `app.TestInstancesShareAPostgresStore` (integration) |

Project security (govulncheck and Dependabot run in CI; signed releases, SBOM and `SECURITY.md`)
is completed with the release work in roadmap phase 12.
