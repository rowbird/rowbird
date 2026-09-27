# ADR-0009: Scheduler polls next_run_at in the database

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

An in-memory cron loses state on restart and duplicates work across instances.

## Decision

Reports store next_run_at. The scheduler polls every few seconds and claims due reports atomically (SQLite single-writer; Postgres FOR UPDATE SKIP LOCKED), creating a pending run and advancing next_run_at in the same transaction. Runs table is the queue.

## Consequences

Restart-safe, multi-instance without leader election, simple misfire handling. Minimum granularity ~1 minute (tick 5s).
