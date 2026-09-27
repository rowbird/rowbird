# ADR-0021: Locale-aware outputs, exact numbers and golden files

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

Formatters turn a run's result into files and message bodies for people in English and Portuguese.
Numbers must never lose digits (money is never converted to float), spreadsheets must open the
files correctly in each language, and every format needs tests that catch any change in output.

## Decision

- `internal/format` holds what formatters share. Values read by people (PDF, HTML, Markdown,
  Telegram) use the reader's language: `1,234.56` and `2026-09-25 14:30` in English, `1.234,56` and
  `25/09/2026 14:30` in Portuguese. Numbers are regrouped from their decimal text, never through
  float64. Datetimes with a time zone are shown in the report's; datetimes without one as stored.
- Files for programs keep a technical form: JSON uses ISO dates and decimals as strings; CSV uses ISO
  dates and, by default, the language's decimal separator without grouping (a semicolon separator
  and a byte order mark in Portuguese), so that Excel opens it as numbers.
- XLSX writes integers and decimals of up to 15 significant digits as numbers (the text stored in
  the file is exactly the decimal); longer ones are text, since Excel keeps only 15 digits.
- Output texts ("Generated on", "Showing N of M") live in `internal/format/locales`.
- Every formatter passes `internal/plugin/formattest` and compares its output with golden files in
  `testdata/golden`, one per case and language, rewritten with `go test -update`. Text formats are
  compared byte for byte; XLSX through a text dump of cells, types and number formats; PDF through
  maroto's component tree and a summary of pages and page size, because the file embeds fonts.
  Golden files are marked `-text` in `.gitattributes` so that line endings and byte order marks
  survive.

## Consequences

Outputs are predictable and reviewed as plain text in pull requests. A deliberate change to a
format means regenerating its golden files and reading the diff. New languages need their entry in
`internal/format` and in the catalogs.
