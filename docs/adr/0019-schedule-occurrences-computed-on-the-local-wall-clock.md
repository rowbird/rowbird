# ADR-0019: Schedule occurrences computed on the local wall clock

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

Reports run on cron schedules in their own time zone, and the spec fixes how daylight saving
behaves: a local time that does not exist runs at the next valid instant, and a local time that
happens twice runs once. robfig/cron's `Next` does not guarantee either: for times inside a gap Go's
`time.Date` picks an offset that is "not guaranteed", and a repeated hour can fire twice. The UI also
needs a readable description of any expression, in English and Portuguese.

## Decision

- robfig/cron v3 only parses: five fields plus `@hourly`, `@daily`, `@weekly`, `@monthly` and
  `@yearly`. Seconds, `@every` and inline `TZ=` prefixes are refused, so the minimum interval is one
  minute and the time zone is always the report's.
- `internal/schedule` computes occurrences itself. It walks days and the matching hours and minutes
  as local wall times and resolves each one to an instant: an ambiguous time resolves to its first
  occurrence (and the second is never used), a time inside a gap resolves to the instant the gap
  ends. The first instant strictly after "now" is the next run. Standard cron day semantics apply
  (day of month or day of week when both are restricted).
- An expression with no occurrence within eight years (such as 30 February) is invalid.
- Descriptions come from `github.com/lnquy/cron` (a port of cRonstrue) with 24-hour times, in `en`
  and `pt-BR`; descriptors are described through their five-field form.

## Consequences

DST behavior is explicit and covered by table tests for São Paulo (historical), New York and London.
The walk is a little slower than bit tricks but bounded, and it runs once per report per slot. Only
five-field expressions are supported; Quartz extensions (`L`, `W`, `#`) are refused by the parser.
