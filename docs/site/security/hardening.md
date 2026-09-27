# Hardening

Rowbird holds credentials to your databases and sends data out of them, so treat it like any
system with production access. Most protections are on by default; this page lists them and the
few settings that are yours to choose.

## Checklist

- [ ] Serve it over HTTPS and set `ROWBIRD_BASE_URL` to the `https://` URL.
- [ ] Set `ROWBIRD_SETUP_TOKEN` before the first start on a reachable server.
- [ ] Provide the master key from a secret (`ROWBIRD_MASTER_KEY_FILE`) and keep a copy elsewhere.
- [ ] Give each connection a database login that can only read, limited to the tables it needs.
- [ ] Set `ROWBIRD_TRUSTED_PROXIES` to your proxy, and only your proxy.
- [ ] Require 2FA for everyone, or use OIDC with your provider's 2FA.
- [ ] Protect `/metrics` with `ROWBIRD_METRICS_TOKEN` if it is reachable from outside.
- [ ] Consider `ROWBIRD_NETWORK_POLICY=block-private` (below).
- [ ] Schedule backups, upload them off the server, and test a restore.

## Your databases

- Queries run in **read-only transactions** where the database supports it, with a server-side
  **timeout** and a **row limit** per connection.
- One statement per query unless the connection explicitly allows more (off by default). The check
  ignores literals and comments. SQL Server does not need `;` between statements, so there the
  read-only login is what really protects you.
- Parameters are always **bound** by the driver, never pasted into SQL.
- Rowbird warns when a connection's login can write.
- SQLite connections can only open files under `ROWBIRD_SQLITE_DIRS`, read-only, and never
  Rowbird's own database.
- SSH tunnels pin the server's host key on the first connection; a changed key is refused.

## Secrets

Credentials are encrypted with AES-256-GCM under the master key and never returned by the API
(it answers `"configured": true`). They are kept out of logs, error messages, exports (which write
`${env:...}` placeholders) and notifications.

## Network policy {#network-policy}

With `ROWBIRD_NETWORK_POLICY=block-private`, connections, channels (HTTP and SMTP) and AI providers
cannot reach private (RFC 1918), loopback, link-local and cloud metadata addresses
(`169.254.169.254`), IPv6 included. DNS is resolved once and the resolved address is checked, so a
name cannot switch to a private address later. Use it when editors or admins should not be able to
probe your internal network through Rowbird. It is `open` by default because most self-hosted
databases live on private networks.

## What Rowbird sends out

- **Data** goes only to the channels you configure.
- **Files** in messages: HTML is escaped, and CSV and Excel cells starting with `=`, `+`, `-`, `@`,
  a tab or a carriage return are neutralized against formula injection.
- **Links** are 256-bit random tokens stored as hashes, with an expiry (1 hour to 90 days),
  optional sign-in, revocation and a download log.
- **Webhooks** are signed with HMAC-SHA256; see [Webhook payloads](/reference/webhooks).
- **AI providers** receive the request and the database schema, never rows; excluded tables are
  left out entirely. The assistant is off until an admin configures it.
- **No telemetry.** The only call Rowbird makes on its own is the daily update check to
  `api.github.com`, which you can turn off.

## The web application

Strict Content Security Policy, `X-Frame-Options: DENY`, HSTS over HTTPS, `HttpOnly` and
`SameSite=Lax` cookies, CSRF tokens on state-changing requests, and no CORS. Every data access is
scoped in the storage layer and every API operation declares the role and scope it needs.

## Security log

Sign-ins, 2FA and passkey changes, OIDC links, API keys, user and role changes, connection and
channel changes, settings, imports and key rotations are recorded in Settings > Security for 365
days.

## Reporting a vulnerability

Please write to security@rowbird.dev or use GitHub's private vulnerability reporting. See
[SECURITY.md](https://github.com/rowbird/rowbird/blob/main/SECURITY.md).
