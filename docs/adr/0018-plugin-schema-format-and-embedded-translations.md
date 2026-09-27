# ADR-0018: Plugin schema format and embedded translations

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Plugins must describe their configuration so that the UI renders forms, the API validates input,
YAML import checks files and secrets are handled, all without per-plugin frontend code (ADR-0004).
Full JSON Schema is large and unordered; a custom format would be unfamiliar. Plugin labels are i18n
keys, but if their texts lived in the web catalogs, every new plugin would still need a frontend
change.

## Decision

- Plugins declare fields with a small Go type that serializes to a JSON Schema subset: `type`,
  `enum`, `default`, numeric and length limits, `format`, plus Rowbird extensions: `x-order`,
  `x-secret` (also emitted as `writeOnly`), `x-group`, `x-multiline`, `x-show-if`, `x-label` and
  `x-help`. The same Go type validates values on the server, so the UI and the API cannot disagree.
- Each plugin package embeds `locales/<lang>.json` (flat key to text) with the texts of its labels,
  help and enum options. `GET /api/v1/plugins` returns every plugin's catalogs; the UI merges them
  into vue-i18n. The i18n check finds every `locales` directory, so plugin catalogs must be complete
  in every language like the core ones.
- Texts shared by several plugins (host, port, TLS, SSH) live in one common catalog that connectors
  merge into theirs.

## Consequences

A plugin is one Go package with its schema, code, translations and conformance test. Validation is
limited to the subset; a plugin that needs a cross-field rule checks it in its own code. Keeping
translations as Go-embedded JSON keeps them out of Go source, where English-only linters would flag
other languages.
