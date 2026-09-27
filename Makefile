SHELL := /bin/bash

VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/rowbird/rowbird/internal/version.Version=$(VERSION) \
	-X github.com/rowbird/rowbird/internal/version.Commit=$(COMMIT) \
	-X github.com/rowbird/rowbird/internal/version.Date=$(DATE)

PNPM := pnpm --dir web
# Run with `go run` instead of a go.mod tool: its dependency graph (Hugo and more) would bloat go.mod.
AIR  := github.com/air-verse/air@v1.67.4

.PHONY: help dev generate test test-integration test-e2e lint build i18n-check web-deps snapshot release-check docs-dev docs-build

help: ## List available targets
	@grep -hE '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  %-18s %s\n", $$1, $$2}'

web/node_modules: web/package.json web/pnpm-lock.yaml
	$(PNPM) install --frozen-lockfile
	@touch web/node_modules

web-deps: web/node_modules ## Install web dependencies

dev: web/node_modules ## Backend with live reload + Vite dev server (proxy /api)
	@mkdir -p .data
	@trap 'kill 0' EXIT; \
		ROWBIRD_DATA_DIR=.data ROWBIRD_BASE_URL=http://localhost:5173 ROWBIRD_UPDATE_CHECK=false go run $(AIR) -c .air.toml & \
		$(PNPM) dev & \
		wait

generate: web/node_modules ## Regenerate Go server stubs + TS client from api/openapi.yaml
	go tool oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml
	$(PNPM) run generate

test: web/node_modules ## Unit tests (Go + Vitest)
	go test -race ./...
	$(PNPM) test

test-integration: ## Integration tests with testcontainers (needs Docker; ROWBIRD_TEST_MSSQL=1 adds SQL Server)
	go test -race -tags integration ./...

test-e2e: build ## Playwright against a built binary (needs Docker for Mailpit and versitygw)
	$(PNPM) exec playwright install $(if $(CI),--with-deps,) chromium
	$(PNPM) test:e2e

lint: web/node_modules ## golangci-lint + eslint + vue-tsc
	golangci-lint run ./...
	$(PNPM) lint
	$(PNPM) typecheck

i18n-check: ## Verify that every locale has the same keys as English
	go run ./scripts/i18ncheck

build: web/node_modules ## Web build, embed, single binary in ./bin/rowbird
	$(PNPM) build
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/rowbird ./cmd/rowbird

release-check: ## Validate the GoReleaser configuration
	goreleaser check

snapshot: ## Build every release artifact and the image locally (dist/, local Docker); nothing is signed or published
	goreleaser release --snapshot --clean --skip=sign

docs/site/node_modules: docs/site/package.json docs/site/pnpm-lock.yaml
	pnpm --dir docs/site install --frozen-lockfile
	@touch docs/site/node_modules

docs-dev: docs/site/node_modules ## Docs site (docs.rowbird.dev) with live reload
	pnpm --dir docs/site dev

docs-build: docs/site/node_modules ## Build the docs site into docs/site/.vitepress/dist (fails on dead links)
	pnpm --dir docs/site build
