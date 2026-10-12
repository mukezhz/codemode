# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

@AGENTS.md

## What this is

Code mode: protobuf RPCs become capabilities a model composes inside a sandbox. The model writes one program (JS/Starlark/Lua) instead of N tool calls; the sandbox exposes only `codemode.execute(name, args)`, which calls back into Go, where the generated registry and role annotations are checked before anything runs.

Flow: protos → `buf generate` (Go carries its own descriptors, so `registry.FromFiles` works at runtime) → registry → capability store/vector search picks relevant capabilities → LLM provider writes program → sandbox engine runs it → `agent/` orchestrates (with program caching and streaming). End-to-end walkthrough: `docs/content/flow.md`.

## Commands

- `mise install` — pinned tools (buf, node, pnpm)
- `make generate` — regen Go + `examples/taskreport/registry.json`; a test asserts the file path and `FromFiles` path agree
- `make test` — buf lint, `go test ./...`, `scripts/check-no-env.sh`, `scripts/check-consumer.sh`
- Single test: `go test ./agent -run TestName -count=1`
- `make test-env` — library packages may read env only in their `env.go`
- `make demo-todo` — runs without API keys or DB (keyword search fallback, fixed program)
- `make demo-web BASE_URL=... API_KEY=... MODEL=...` — taskreport web app on :8080
