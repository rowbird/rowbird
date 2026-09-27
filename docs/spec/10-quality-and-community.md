# 10: Quality & community

## Tests

- **Unit:** scheduler (cron, timezones, DST, misfire), params parser/binder, conditions,
  formatters (golden files), templates, crypto, authz matrix.
- **Integration** (`-tags integration`, testcontainers): PostgreSQL, MySQL, MariaDB, SQL Server,
  SQLite; Mailpit (SMTP), MinIO (S3); fake HTTP servers for Slack, Telegram, Discord, webhook,
  Uptime Kuma; SSH server container for tunnels.
- **Conformance suites** per plugin kind (e.g. connectors: type normalization incl. exact decimals,
  timeout, cancel, read-only enforcement, schema, row limit, multi-statement rejection).
- **Store:** every repository test runs against both SQLite and Postgres.
- **Frontend:** Vitest for components/composables; **Playwright** E2E: setup → connection → query →
  report → delivery received in Mailpit; link download; import/export.

## CI (GitHub Actions)

On PR: lint (golangci-lint, eslint, vue-tsc), unit, integration, E2E, `govulncheck`, generated-code
drift check (`make generate` then `git diff --exit-code`), i18n key completeness check.

## Releases

GoReleaser (ADR-0029, `.goreleaser.yaml`): binaries (linux/darwin/windows × amd64/arm64),
multi-arch distroless images on GHCR (`X.Y.Z`, `X.Y`, `X`, `latest`; prereleases only their exact
tag), checksums, cosign keyless signatures (checksums and images), an SBOM per archive, a draft
release with the changelog from Conventional Commits. SemVer tags `vX.Y.Z`. The release workflow
only runs in `rowbird/rowbird`; `make snapshot` builds everything locally without signing or
publishing. CI also checks the GoReleaser configuration and builds the docs site.

## Documentation

VitePress site in `docs/site`, published at `docs.rowbird.dev` (English; pt-BR later): install, quick start, concepts, connectors & destinations
reference (generated from plugin metadata where possible), configuration, config-as-code, API
reference, CLI, security, **plugin development guide** (full example).

## Try it in one minute

`deploy/demo/docker-compose.yml`: Rowbird + Postgres with fictional shop data + Mailpit, and a
GitOps directory (`deploy/demo/config`) applied right after the setup wizard. `docker compose up`,
create the admin, and reports land in Mailpit's inbox. A Go test applies the directory so it
cannot drift from the YAML format. `deploy/compose` is the production example.

## Community files

`README.md` (GIF, one-command install, supported connectors/destinations with icons, "why not
Metabase/Redash for this?"), `LICENSE` (Apache-2.0), `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
`SECURITY.md` (security@rowbird.dev), issue templates (bug, feature, new connector, new destination),
PR template, labels (`good first issue`, `help wanted`, `plugin`), launch issues requesting new
connectors/destinations.
