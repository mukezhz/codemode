# 0009 — Plans are rows, not processes

**Status:** accepted
**Date:** 2026-11-16

## Context

A held write waits for a person, and a person may answer tomorrow. Three shapes
can hold that wait: an actor per plan, a durable workflow engine that suspends
the program, or a row in a database.

An in-process actor dies with the process and lives on one replica, so it needs
a database and routing anyway. A workflow engine gives true continuations — the
program resumes with the real result of the approved write — but it needs its
own infrastructure and a sandbox that replays deterministically. Neither is
needed for the cases in front of us: the program has already finished, and what
waits is a list of calls.

## Decision

A plan is a row with a status, a version and a history. `agent.Plans` moves it
through `pending → applying → applied | failed`, or to `rejected` or `expired`,
and every move is a compare-and-set on the version, so the database serialises
decisions across replicas. Expiry is checked on read. Applying records each
call's result before the next, so a crash can be resumed rather than lost.

The storage port is four methods. `agent/sqlplans` implements it once on
`database/sql` for SQLite and Postgres; a host on Cloudflare can implement it on
a Durable Object without touching `agent`.

Every run gets an ID that its error, its events and its plan all carry.
Observability is one callback, `OnEvent`; `LogEvents` adapts it to `slog`, and
telemetry SDKs stay in the host.

## Consequences

- `NewMemoryPlans` and `ApplyPlan` are replaced by `NewPlans` and
  `Plans.Approve` before either was released.
- A held write still returns `{}` to the program. Continuations would need the
  workflow-engine shape; revisit when a host needs chained approvals.
- `Resume` re-runs the call that was in flight at the crash. Handlers of writes
  that need approval should be idempotent, or the host checks before resuming.
- A plan is approved by its owner. Approval by another role is a later change to
  `Plans`, not to the store.
