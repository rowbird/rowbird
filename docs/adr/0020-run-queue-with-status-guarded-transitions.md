# ADR-0020: Run queue with retries in the queue and status-guarded transitions

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

ADR-0009 makes the runs table the queue. Retries with exponential backoff, cancellation from any
instance, overlap policies and recovery of runs whose instance died all need to change a run's state
safely while a worker, the scheduler, a user and other instances may touch it at the same time.
Optimistic versions would make every user action conflict with the worker that owns the run.

## Decision

- A run carries `available_at`. Workers claim the oldest pending run whose `available_at` has
  passed, in a transaction (`FOR UPDATE SKIP LOCKED` on Postgres, the write lock on SQLite), and
  record their `instance_id`. A failed attempt that may be retried goes back to `pending` with the
  next attempt number and `available_at` pushed by the backoff, so no worker waits on a timer.
- State changes are guarded by the current status (and, for the worker's own writes, by
  `instance_id`), not by the optimistic version: finishing only succeeds while this instance still
  owns a running run, so an instance that lost a run can never overwrite its recovery.
- Scheduled runs of a report whose previous run is still going follow the overlap policy at claim
  time, with the report row locked on Postgres. Manual runs are never held back.
- Every instance records a heartbeat in `instances`. Running runs of an instance silent for three
  ticks (at least 30 seconds) are recovered as `run.instance_lost` and retried per policy. SQLite
  deployments run a single instance, so at start every run left running by another instance is
  recovered at once.
- Cancelling a running run sets `cancel_requested_at`; the owning worker cancels the query's context
  (at once when it is in the same process, on its next tick otherwise).
- Scheduler updates to a report (`next_run_at`, the failure streak, the automatic pause, the last
  result hash) do not bump its version, so they never conflict with a user editing it.

## Consequences

The queue needs no extra tables or timers and survives restarts. A crash loses at most the work of
the runs in flight, which are retried. Recovery takes up to three ticks on Postgres. Status guards
are simple to reason about but every transition must name the statuses it starts from.
