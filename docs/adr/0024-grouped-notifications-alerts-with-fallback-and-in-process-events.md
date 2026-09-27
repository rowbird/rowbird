# ADR-0024: Grouped notifications, alerts with a fallback, and in-process events

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

When a channel or a report starts failing someone must learn about it once, not once per run, and
learn again when it recovers (docs/spec/03-flows.md, section 8). The channel that fails may be the
one alerts would go through, so there must be a second path. The UI polled every two seconds
while a run was active; ADR-0010 chose server-sent events to replace that, but did not say how
events reach clients when several instances share a Postgres store.

## Decision

- **One component, `internal/notify`,** learns facts through small interfaces defined where they
  are produced: the runner tells it about runs (every status change, and the outcome of runs that
  count toward a report's health), the channels service about every recorded channel outcome with
  the status before it, the reports service about pauses and resumes, and the delivery engine
  about resends. A nil notifier does nothing, so each package still works and tests alone.
- **Grouping by an open notification.** A problem has a group key (`channel:<id>:failing`,
  `report:<id>:failing`, `report:<id>:paused`). While a user has an open notification of the
  group, a new occurrence increments its count, moves `last_at` and makes it unread again. A unique
  index on the open group enforces one per user. The first success resolves the group and adds a
  recovery notice, resolved from the start. There is no time window: a problem is one
  notification however long it lasts.
- **One row per recipient.** Notifications are written for each active admin (and the report's
  owner when asked), instead of one shared row with `user_id` null. Read state is then per user and
  listing needs no join; a user who becomes admin later does not see earlier notifications.
- **Alerts go out once per group.** Opening a group and resolving it send an alert; counted
  occurrences do not. The alert goes to the primary channel; if sending there fails, or the alert
  is about the primary itself, to the fallback. Each alert channel stores the delivery options of
  its destination (recipients, chat). Destinations render alerts from a `MessageAlert` (title,
  text, severity, recovered, link) carried in the message; a capability says which destinations
  can (a bucket cannot). Alert sends never record channel health, so an alert about a channel, or
  a failing alert, cannot produce another alert.
- **Alerts are sent in the background** from a bounded in-memory queue (100), 30 seconds per try,
  so a slow alert channel never delays deliveries. A crash can lose queued alerts; the in-app
  notification is already stored.
- **Events stay in the process.** A broker per instance fans events out to that instance's SSE
  subscribers, filtered by workspace and, for notifications, by user. Events carry only what
  changed; clients refetch. The UI polls every 15 seconds while it shows something unfinished (2
  seconds while the stream is down), which covers runs executed by other instances. Postgres
  LISTEN/NOTIFY was considered and left out: it needs a dedicated connection with reconnection
  and the polling fallback would still be needed for a dropped stream.
- **The SSE operation is in the OpenAPI document** (`text/event-stream`), so authorization applies
  as for any operation; the strict handler returns a response object that streams and flushes, and
  closing the broker at shutdown ends every stream.

## Consequences

People get one alert per problem and one per recovery, even when the alert channel is the broken
one, as long as the fallback works. The notification table grows with the number of admins, which
is small. With several instances, real-time updates for runs on other instances arrive within the
slow polling interval rather than at once. Adding a destination means deciding whether it supports
alerts and rendering `MessageAlert`; the conformance suite checks both paths.
