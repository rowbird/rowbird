# ADR-0022: Shared handling of plugin configurations with secrets

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

Connections and channels both store a plugin configuration validated by the plugin's schema, where
some fields are secrets (passwords, tokens, webhook URLs, HMAC keys). Each needs the same rules
(docs/spec/05-api.md and 07-security.md): secrets encrypted at rest, never returned by the API,
the `{ "configured": true }` placeholder meaning "unchanged" on updates, `null` clearing a secret,
and secret values removed from the error messages of drivers and destinations before they are
logged or stored. Phase 2 implemented this inside the connections service. Phase 6 adds channels,
and later phases add AI provider keys and secret settings. Copying the code per entity would let
the rules drift apart, and a missed detail here leaks a secret.

## Decision

- `internal/secretconfig` holds the rules for every entity that stores a plugin configuration with
  secrets. A `Codec` is created per kind of entity (`connection`, `channel`) with the keyring.
- `Seal` splits validated values by the schema's `x-secret` fields into plain configuration (a JSON
  column) and one encrypted JSON blob (`secrets_enc`). The associated data is
  `<kind>:<id>:secrets`, so a ciphertext copied to another row, or to another kind of entity with
  the same id, fails to decrypt.
- `Values` rebuilds the full configuration for the plugin, restoring the schema's types (JSON
  decoding turns integers into floats).
- `Resolve` applies the update rules: an absent key or the placeholder keeps the stored secret,
  `null` removes it, a string replaces it. The same function resolves placeholders when an unsaved
  form is tested against a saved entity (`connection_id`, `channel_id`).
- `Configured` says which secret fields hold a value, so the API can return
  `{ "configured": true }` for each of them and nothing else.
- `Scrub` removes every secret value of the configuration from a message and then applies the log
  redactor (credentials in URLs, among others). Driver and destination errors go through it before
  they are logged or stored on delivery attempts and channel health; the error code is stored and
  shown on its own.
- Plugin schemas remain the single source of which fields are secret: there is no per-entity list.

## Consequences

A new entity with a secret-bearing configuration gets encryption, placeholders and scrubbing by
using a `Codec`, and the secret leak tests written for connections apply to it too. Changing the
associated data format would make existing ciphertexts unreadable, so it is fixed; key rotation
(roadmap phase 10) re-encrypts with the same associated data. A secret can only be detected in an
error message if it is one of the configured values, so plugins still must not echo other
sensitive data (such as full request bodies) into errors.
