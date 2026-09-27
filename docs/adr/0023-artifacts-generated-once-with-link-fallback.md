# ADR-0023: Artifacts generated once per run, link fallback and resends

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

A report can have up to 20 deliveries, several asking for the same file (an XLSX by email and on
Slack). Destinations have different limits: email servers usually refuse messages above 20 MB,
Discord webhooks above 8 MB, webhooks and Uptime Kuma take no files at all. Failed deliveries must
be resent later without running the query again (docs/spec/03-flows.md, section 6), possibly after
the run's spool is gone, and resends must not make the run history confusing. Files must be
reachable by any instance, and shared links must work while the run's files exist.

## Decision

- **Artifacts.** Each file format a run's deliveries need is generated once, on first use, from the
  spool, stored in artifact storage and recorded as an `artifacts` row (unique per run and format).
  Every delivery and every resend of the run uses that row. Inline renderings (the table in an
  email, a Slack message) are stored as artifacts too, keyed by formatter, row limit, character
  limit and whether a link is included, so that a resend shows the same table. Artifacts expire
  after `retention_artifacts_days` (30 by default); the run's `GET /runs/{id}/result` serves the
  stored file when there is one.
- **Storage is configuration, not a plugin.** One backend (`local` or `s3`) is chosen for the whole
  instance with `ROWBIRD_STORAGE_BACKEND`. It has no per-workspace settings, no form and no
  translations, so registering it as a plugin would add a schema and a registry entry nobody uses.
  The contract (`plugin.Storage`) stays in `internal/plugin` so the engine and the links service
  depend only on it, and each backend passes `internal/storage/storagetest`. This amends
  docs/spec/01-architecture.md, which listed `storage` as a plugin kind.
- **Link fallback.** In `attachment` mode files are attached while their total fits the
  destination's `max_attachment_bytes`; files beyond it, or all files when the destination cannot
  attach, are shared as links instead. The message says so and the attempt records
  `fallback_to_link`. A destination that takes neither files nor links is not offered those modes.
  Links need `ROWBIRD_BASE_URL`; without it the `link` mode is refused when a delivery is saved and
  a fallback fails the delivery with `delivery.base_url_missing`.
- **One attempt row per delivery, reused by resends.** A run records one `delivery_attempts` row per
  delivery. A resend claims the failed row (status back to `sending`, guarded so that two resends
  cannot run at once), adds its tries to `attempts` and overwrites the outcome. The run page shows
  one line per delivery with its latest result, instead of a growing list. Resends do not create
  runs, so the `retry` trigger stays reserved.
- **Partial runs keep their spool.** Runs that succeeded, were partial or were skipped keep the spool
  for 24 hours. A partial run needs it because a resend may need a format that no delivery had
  generated before it failed (the failure may have happened while generating it).
- **A partial run becomes a success** once a resend leaves every attempt `sent` or `skipped`.
- **Resends create new links.** Each message builds its links with new tokens, because a token is
  stored only as a hash and cannot be sent again. Links from the failed attempt stay valid until
  they expire or are revoked; they may have reached the recipient if the destination failed after
  accepting part of the message.
- **Retries inside a delivery** are short (3 tries, 2 s and 4 s apart, each send bounded by five
  minutes) and only for errors the destination marks as transient. Longer outages are handled by
  resends, single or in bulk for a channel.

## Consequences

Formats are generated at most once per run, whatever the number of deliveries and resends, and any
instance sharing the storage can serve files and links. A failed delivery can be resent for as long
as its artifacts exist; a format first needed by a resend can be generated only while the spool
lasts (24 hours) and on the instance that ran the report, otherwise the resend fails with
`delivery.result_unavailable`. The run history does not show every resend as a separate attempt;
the tries count and the latest error are what remains. Several valid links can exist for the same
file after resends, and all of them appear on the Links screen where they can be revoked. Switching
the storage backend makes earlier artifacts unreachable.
