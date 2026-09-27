# ADR-0001: Go for the backend

- **Status:** Accepted
- **Date:** 2026-09-25

## Context

Rowbird must install in one command and run as a single process on anything from a Raspberry Pi to Kubernetes. The workload is scheduling, concurrent I/O, timeouts and many DB drivers.

## Decision

Use Go (latest stable) for the backend, compiled to a single static binary.

## Consequences

Single-binary distribution and tiny images; goroutines/context fit scheduling and cancellation; mature drivers for all v1 databases. Contributors need Go for backend plugins.
