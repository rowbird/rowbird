# ADR-0027: Passkeys as a second factor of their own, and OIDC sign-in that owns 2FA

- **Status:** Accepted
- **Date:** 2026-09-27

## Context

Phase 10 completes authentication (docs/spec/07-security.md): passkeys as primary or second factor,
and OIDC with PKCE, auto-provisioning and a domain allowlist. Three questions shape both: whether a
passkey satisfies a workspace that requires 2FA, what happens when an OIDC identity arrives with the
email of an existing local user, and whether OIDC sessions must also pass Rowbird's second factor.
Both features also need state between two HTTP requests (a WebAuthn challenge, an OAuth state and
verifier) that must work when several instances share a Postgres store.

## Decision

- **A passkey counts as 2FA.** Passwordless sign-in uses discoverable credentials with user
  verification required (PIN or biometrics), which is itself two factors. A user with a passkey is
  not asked to enroll TOTP when the workspace requires 2FA, and the password login offers the
  passkey as its second step. Registering one requires the current password, like enabling TOTP.
- **The relying party is the public URL.** `ROWBIRD_BASE_URL` gives the RP ID (its host) and the
  origin; without it passkeys are off and the UI says why. WebAuthn does not accept an IP address as
  RP ID, so deployments reached by IP use OIDC or TOTP.
- **Ceremonies in the database.** The WebAuthn session data is stored in `webauthn_challenges`,
  found by the hash of a token given to the browser, single use and valid 5 minutes. Any instance
  can finish a ceremony another started.
- **Credential records as JSON.** A passkey row keeps the library's credential record as JSON plus
  the columns needed for lookups (`credential_id`) and display, so library upgrades that add fields
  need no migration. A signature counter that goes backwards refuses the sign-in (possible clone).
  An admin's 2FA reset removes passkeys with the TOTP secret, since a lost phone takes both.
- **OIDC links only verified emails.** The user is found by `(issuer, subject)`. Otherwise an
  existing user with the same email is linked only when the provider says `email_verified` and the
  user has no other identity; a link is never moved silently. Otherwise, with auto-provisioning, a
  user is created with the configured role, which can be viewer or editor but never admin, and no
  password. A domain allowlist, when set, admits only verified emails in those domains.
- **OIDC sessions skip Rowbird's 2FA.** The provider decides how users authenticate (its own MFA,
  device policies); asking again would push people away from single sign-on. The session records
  `auth_method = oidc` and is exempt from the 2FA and temporary-password restrictions.
- **OIDC flow state in an encrypted cookie.** State, nonce, PKCE verifier and the app path to return
  to travel in an `HttpOnly`, `SameSite=Lax` cookie encrypted with the master key, valid 10 minutes
  and scoped to `/api/v1/auth/oidc`, so no table and no sticky sessions are needed. Errors return
  to the login page as a code only.
- **One provider per workspace, in settings.** The configuration is a workspace setting managed in
  Settings > Security; the client secret is a secret setting (bound to `setting:oidc:<workspace>`)
  that key rotation covers. Requests to the provider go through the network policy.

## Consequences

- Passkeys and OIDC both need `ROWBIRD_BASE_URL`; the setup status tells the login page what to
  offer.
- A compromised identity provider can sign in its users, including admins linked by email; admins
  who do not want that can leave OIDC off or restrict domains.
- A master key rotation invalidates OIDC flows in progress (they last 10 minutes) along with CSRF
  tokens.
- Tests use a software authenticator (`internal/auth/webauthntest`) and a fake provider
  (`internal/auth/oidctest`); the E2E uses Chromium's virtual authenticator and a fake provider in
  the fake server.
