# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

@AGENTS.md

## What this is

Code mode: typed Go functions, protobuf RPCs or MCP tools become capabilities a model composes inside a sandbox. The model writes one program (JS/Starlark/Lua) instead of N tool calls; the sandbox exposes only `codemode.execute(name, args)`, which calls back into Go, where the generated registry and role annotations are checked before anything runs.

Flow: a source (typed Go functions via `capability/gosource`, protos via `buf generate` + `registry.FromFiles`, or MCP tools via `capability/mcpsource`) → `codemode.Source` → capability store/vector search picks relevant capabilities → LLM provider writes program → sandbox engine runs it, every call validated against its shape → `agent/` orchestrates (program caching, streaming, typed answers via `RunAs`, approval plans). End-to-end walkthrough: `docs/content/flow.md`.

## Ports and adapters

| Port | Adapters |
|---|---|
| `capability.Store` | `capability/sqlitestore`, `capability/pgstore` |
| `program.Cache` | `program.New` (semantic), `program.NewExactCache` |
| `program.Store` | `program.NewMemoryStore`, `capability/sqlitestore`, `capability/pgstore` |
| `agent.PlanStore` | `agent.NewMemoryPlanStore`, `agent/sqlplans` (SQLite and Postgres on `database/sql`) |
| `llm.Client`, `llm.Streamer` | `llm/claude`, `llm/openai` |
| `sandbox.Runner` | `sandbox/goja`, `sandbox/starlark`, `sandbox/lua`, `sandbox/subprocess` |
| `sandbox.Isolation` (registered) | `sandbox/subprocess` (`process`); in-process is built in |

`agent/` is the core and depends only on ports. Every adapter declares `var _ <Port> = (*impl)(nil)` beside its type; add one to any new adapter. Find implementations with `git grep 'var _ sandbox.Runner'`.

## Commands

- `mise install` — pinned tools (buf, node, pnpm)
- `make generate` — regen Go + `examples/taskreport/registry.json`; a test asserts the file path and `FromFiles` path agree
- `make test` — buf lint, `go test ./...`, `scripts/check-no-env.sh`, `scripts/check-consumer.sh`
- Single test: `go test ./agent -run TestName -count=1`
- `make test-env` — library packages may read env only in their `env.go`
- `make demo-todo` — runs without API keys or DB (keyword search fallback, fixed program)
- `make demo-shop` / `make demo-combined` — the quickstart, and one program over Go, proto and MCP sources; both run without keys
- `make demo-web BASE_URL=... API_KEY=... MODEL=...` — taskreport web app on :8080
