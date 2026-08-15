# Notes

Internal writing. Nothing here is published.

`docs/` is the website: everything in it ships to readers. This directory is the
other half — the writing that shapes the work rather than describing the
finished thing. It has no build step and no tooling; GitHub renders it, including
`mermaid` fences, which is enough.

| Directory | What goes in it |
| --- | --- |
| `prd/` | Product requirement docs. What we are building and why, before it exists. |
| `milestones/` | Scope and sequencing. What ships when, and what is deliberately out. |
| `decisions/` | Architecture decision records. Why we chose X over Y, kept even after X is obvious. |

## Why the split exists

A PRD is not user documentation. If it lives under `docs/` it either gets
published — which is wrong, it describes intent rather than behaviour, and it
goes stale the moment the thing ships — or it needs an exclusion rule that
someone eventually forgets.

Keeping the boundary at the directory level means the rule is structural rather
than remembered: `docs/` publishes, `notes/` does not.

## When a note graduates

A note is finished when the thing it describes exists. At that point its durable
content belongs in `docs/` as documentation, and the note stays as a record of
why. Do not update a PRD to match what was built — write the docs page instead,
and leave the PRD as the historical statement of intent.

Decisions are the exception: an ADR is never updated, only superseded by a later
one that links back to it.

## Naming

Number decisions sequentially and never renumber: `0003-postgres-backend.md`.
PRDs and milestones read better with dates: `2026-08-todo-example.md`. Either
way, the filename should say what it is without opening it.
