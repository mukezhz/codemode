# 0002 — Split backends into registered packages

**Status:** accepted
**Date:** 2026-08-15

## Context

Preparing the module for use as a library exposed a disqualifying defect:

```
$ go list -deps ./...     # importing only codemode
github.com/jackc/pgx
github.com/mattn/go-sqlite3

$ CGO_ENABLED=0 go build ./...
capability/sqlite.go:49:21: sqliteConn.LoadExtension undefined
```

`codemode` imports `capability` for `Registry`. `capability` held `sqlite.go`
and `postgres.go` in the same package, so every consumer linked both drivers and
none could produce a static binary. Distroless and scratch images are the norm
for Go services; this ruled the library out for them, and the error names a
symbol rather than the cause.

The same shape, less severely, applied to `sandbox` (all three interpreters) and
`llm` (both model SDKs).

## Decision

The `database/sql` pattern, applied to all three seams: the interface stays in
the parent package, each implementation moves to its own package and registers
itself from `init`, and the consumer blank-imports what it wants.

```
capability/            Registry, Store, OpenStore, RegisterBackend
capability/pgstore     capability/sqlitestore
llm/                   Client, New, RegisterProvider
llm/claude             llm/openai
sandbox/               Runner, New, RegisterEngine
sandbox/goja           sandbox/starlark        sandbox/lua
```

Importing `codemode` now links no third-party package at all.

Three supporting choices:

**Provider detection is ordered by an explicit `Priority` field**, not by
registration order. Import order is not something a caller should have to reason
about, and a new blank import must not silently change which model an existing
deployment picks.

**The unregistered-backend error names the import path.** A missing blank import
is the predictable failure of this design and it surfaces at runtime, not at
compile time, so the error carries the fix: ``add `_ ".../sandbox/lua"` to your
imports``. An unknown name reads differently and invents no path.

**`scripts/check-consumer.sh` enforces the property.** It builds a throwaway
module against the working tree and fails if `codemode` alone pulls in anything
third-party. It runs in `make test` and CI, so this is a tested invariant rather
than a convention that decays.

## Alternatives considered

**Build tags** (`//go:build codemode_sqlite`). No new packages, but the
env-driven selection would fail silently when a tag is missing, tags do not
compose with `go get`, and every consumer would have to thread them through
their own build.

**Separate Go modules per backend.** Maximum isolation, and it would let a
backend depend on something the core never sees. Rejected for release overhead:
every backend needs its own tag and its own compatibility matrix, for a problem
package boundaries already solve.

**Document the cgo requirement and change nothing.** Cheapest, and wrong: it
concedes static builds for every consumer to avoid a mechanical refactor, and
leaves `LoadExtension undefined` as a new user's first impression.

## Consequences

Import paths changed for every backend, so this had to land before the first
tag. `sandbox.NewGoja` became `goja.New`, `capability.OpenSQLite` became
`sqlitestore.Open`, and `llm.NewEmbedder` moved to `llm/openai` — the embeddings
protocol is OpenAI's, so its package is where it belongs even when Claude writes
the programs.

The shared prompt frame moved to `sandbox/internal/prompt`. It is internal
because it is how these three engines phrase things, not a contract; a `Runner`
implemented elsewhere writes its own `Prompt`. `capability.ScanMatches`,
`EncodeSchemas`, and `DecodeSchemas` went the other way and are now exported,
because a third-party `Store` cannot be written without them.

Tests that span implementations moved to `sandbox/enginetest`, `llm/providertest`,
and external `_test` packages, with shared doubles in
`capability/internal/fixture`. Each interface package keeps a small stub-based
test of its own registration mechanics, which is what let `llm` and `sandbox`
keep the property of linking no vendor SDK even in test builds.
