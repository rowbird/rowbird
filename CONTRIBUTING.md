# Contributing to Rowbird

Thanks for helping. Bug reports, documentation fixes, new connectors and destinations and code are
all welcome. For anything larger than a small fix, open an issue first so we can agree on the
approach before you spend time on it.

## Development setup

You need Go (the version in `go.mod`), Node.js 22 with pnpm, Docker (for integration and E2E
tests) and golangci-lint.

```bash
make dev               # backend with live reload + Vite dev server on http://localhost:5173
make generate          # regenerate Go server stubs and the TypeScript client from api/openapi.yaml
make test              # unit tests (Go + Vitest)
make test-integration  # integration tests with testcontainers
make test-e2e          # Playwright against a built binary
make lint              # golangci-lint + eslint + vue-tsc
make i18n-check        # every locale has the same keys
make build             # single binary in ./bin/rowbird
make docs-dev          # the docs site
```

## How the project is organized

[AGENTS.md](AGENTS.md) has the full set of conventions (Go, frontend, definition of done). It is
written for AI coding agents, and it is just as useful for people.


- `docs/spec` describes the product's behavior, one file per topic. `docs/adr` records
  architectural decisions. Read the relevant files before changing behavior, and update them in the
  same pull request when behavior changes. Do not contradict an accepted ADR without a new one.
- `api/openapi.yaml` is the API contract. **API changes start there**, then `make generate`.
  Never edit generated code by hand; CI fails when it drifts.
- Everything that varies is a plugin (`internal/connector`, `internal/destination`,
  `internal/format`, `internal/condition`, `internal/ai`). See the
  [plugin development guide](https://docs.rowbird.dev/plugins/development).

## Rules that are never bent

- Secrets never appear in plain text in logs, errors, API responses, exports or notifications. Add
  a test when you handle a secret.
- Every data access is scoped by workspace in the store layer.
- Query parameters are always bound by the driver, never concatenated into SQL.
- User databases are read only by default.
- Database content is escaped in HTML and neutralized against formula injection in CSV and Excel.
- AI output is never executed automatically.
- No telemetry.
- No hardcoded user-facing strings: add keys to both `en` and `pt-BR`.

## Pull requests

- Keep them small and focused, with tests: table-driven unit tests, and integration tests (build
  tag `integration`) when I/O is involved.
- Use [Conventional Commits](https://www.conventionalcommits.org): `feat(scheduler): ...`,
  `fix(connector/mysql): ...`, `docs: ...`. Breaking changes use `!` (`feat(api)!: ...`). The
  changelog is generated from them.
- Everything in the repository is written in English (except the `pt-BR` locale files).
- `make lint` and `make test` must pass. CI also runs integration and E2E tests, `govulncheck`, the
  generated code and i18n checks, and the docs build.
- Update the docs site (`docs/site`) when you change something users or operators see.

## Reporting bugs

Use the bug report template and include the Rowbird version (`rowbird version`), how you run it,
what you expected and what happened, with logs. Remove secrets from anything you paste.
Security issues go to [SECURITY.md](SECURITY.md), not to public issues.

## License

By contributing you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
