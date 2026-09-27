# Sign-in: OIDC, passkeys and 2FA

## Passwords

Passwords are hashed with argon2id, must have at least 10 characters and are checked against a list
of common passwords. Failed sign-ins lock the account progressively (after 5 failures, one minute,
doubling up to 15 minutes), and each client IP has a limit too. Unknown accounts, wrong passwords
and disabled accounts get the same answer in the same time.

**Forgot password** sends a one-use link valid for 30 minutes. It needs a system mailer (an email
channel marked as such) and `ROWBIRD_BASE_URL`; the login page offers it only when both are set.

## Two-factor authentication

Each user can enable TOTP (any authenticator app) with 10 one-time recovery codes, and add
passkeys. Admins can **require 2FA** for everyone (Settings > Security): users without a second
factor must set one up at their next sign-in.

## Passkeys

Passkeys (WebAuthn) work as a second factor, or on their own for a passwordless sign-in with the
device's PIN or biometrics. They need `ROWBIRD_BASE_URL`: its host is the relying party, so
**passkeys registered for one host name do not work on another**. Choose the final host name before
people register passkeys.

Users add passkeys in their profile after confirming their password. An admin resetting a user's
2FA removes their passkeys as well.

## OIDC single sign-on

Rowbird signs in with any OpenID Connect provider: Google, Microsoft Entra ID, Okta, Keycloak,
Authentik, Zitadel, GitLab and others. Configure it in Settings > Security:

1. Register an application (a "web" client) at the provider with the redirect URI Rowbird shows:
   `https://rowbird.example.com/api/v1/auth/oidc/callback`. `ROWBIRD_BASE_URL` must be set.
2. Enter the issuer URL (for example `https://accounts.google.com`), the client id and the client
   secret. "Test" reads the provider's discovery document.
3. Choose the scopes (`openid email profile` by default) and the button label.
4. Decide how accounts are matched:
   - A user already linked to the same provider identity signs in.
   - Otherwise an existing user with the same email is linked, but only when the provider says the
     email is verified.
   - Otherwise, with **auto-provisioning** on, a new user is created with the default role
     (viewer or editor, never admin) and no password.
   - **Allowed domains** restrict all of this to verified emails in those domains.

OIDC sign-ins do not ask for Rowbird's second factor: the provider owns the password and 2FA.
Users who also have a password can still sign in with it.

Flow details: authorization code with PKCE (S256); state, nonce and verifier travel in a short-lived
encrypted cookie, so any instance can finish the sign-in.

## Sessions

Sessions last 7 days, extended while used. Users see their sessions in their profile and can sign
out of any or all of them. Changing a password signs out the other sessions; role changes,
disabling a user and admin resets sign the user out everywhere.

## API keys

Admins create API keys in Settings > API keys. A key is shown once, starts with `rbk_`, and is
sent as `Authorization: Bearer rbk_...`. Its scope limits what it can do:

| Scope | Allows |
|---|---|
| `read` | reading everything a viewer can |
| `run` | `read`, plus running reports and AI proposals |
| `write` | `run`, plus creating and changing queries and reports |
| `admin` | everything, including connections, channels, users and imports |

A key never exceeds its owner's current role and stops working when the owner is disabled.

## Recovering access

If every admin is locked out, use the CLI on the server; it talks to the store directly:

```bash
rowbird user reset-password --email admin@example.com   # prints a temporary password
rowbird user disable-2fa --email admin@example.com
rowbird user set-role --email someone@example.com --role admin
rowbird user create --email new-admin@example.com --name "New Admin" --role admin
```

These actions are recorded in the security log as CLI actions.
