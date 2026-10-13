# Repository Guidelines

## Project Structure & Module Organization

Protobuf contracts live in `proto/`, which is also a Go package embedding `codemode.proto` for consumers. `registry/` turns a Buf descriptor set into a registry; `cmd/codemode-registry/` is its CLI. Shared registry types and vector search live in `capability/`, the request pipeline in `agent/`; tests sit beside their Go source. Keep generated descriptor and registry files out of commits. Root configuration includes `buf.yaml`, `mise.toml`, and the `Makefile`.

Every swappable backend is an interface in a parent package and an implementation in a child that registers itself from `init`: stores in `capability/{sqlitestore,pgstore}`, providers in `llm/{claude,openai}`, engines in `sandbox/{goja,starlark,lua}`. **The parent packages must never import a vendor SDK** — that property is what lets a consumer link only what it blank-imports, and `scripts/check-consumer.sh` fails the build if it breaks. Add a backend by adding a package, never by adding a case to a switch in the parent.

Capability policy lives in the protos, not in Go. An RPC that only some roles may call declares `(codemode.roles)`; a service where everything is privileged declares `(codemode.default_roles)` so a new RPC arrives restricted. Never reintroduce a list of privileged capability names in a host package — it is fail-open, and `Registry.Unrestricted()` plus a test is what catches the annotation somebody forgets.

Written material is split by whether it is published. `docs/` is the VitePress site and is self-contained: its own `package.json`, lockfile, and `node_modules`, with pages under `docs/content/` and the sidebar in `docs/.vitepress/config.ts` — add a page there when you add one. Diagrams are ` ```mermaid ` fences, never hand-authored SVG. `notes/` is internal writing — PRDs, milestones, and decision records — and is never published; see `notes/README.md`. Prose belongs in one of those two, not in package comments duplicated across files.

## Build, Test, and Development Commands

Install pinned tools with `mise install`, then use:

- `make generate` — regenerate Go from every proto module and rebuild the taskreport example's `registry.json`.
- `make test` — run Buf lint, all Go tests, and the consumer check; the native vector test skips without its extension.
- `make test-consumer` — verify importing `codemode` alone links nothing third-party and the core packages build with `CGO_ENABLED=0`.
- `SQLITE_VECTOR_EXTENSION=/absolute/path/vector.dylib make test-vector` — exercise real semantic search using sqlite-vector 1.0.0. Use `vector.so` on Linux.
- `make test-postgres` — exercise the pgvector backend against a throwaway container. Requires Docker.
- `make docs` / `make docs-build` — serve or build the VitePress documentation site in `docs/`. Node and pnpm are pinned in `mise.toml`; the Go module itself needs neither.

Each command should work from the repository root and fail with a nonzero exit code on errors.

## Coding Style & Naming Conventions

Format Go with `gofmt` and protobuf with Buf. Use tabs as produced by `gofmt`; exported Go names use `PascalCase`, internal names use `camelCase`, and package names remain short and lowercase. Protobuf messages use `PascalCase` and fields use `snake_case`. Reuse shared definitions from `capability/schema.go`.

## Testing Guidelines

Use Go's standard `testing` package and name tests `TestBehavior`. Keep unit tests deterministic. Tests that need more than one backend live in an aggregate package (`sandbox/enginetest`, `llm/providertest`) or an external `_test` package, so an interface package's own tests do not link a vendor SDK; shared doubles live in `capability/internal/fixture`. Native sqlite-vector tests must read the library path from `SQLITE_VECTOR_EXTENSION`; never search for or load arbitrary extensions. Add one focused regression test for each non-trivial branch or bug fix, including every bug found while running codemode in a host product. No coverage threshold is configured.

## Commit & Pull Request Guidelines

Use Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:`) with a concise imperative subject, for example `feat: add input validation`. Record user-visible changes under `[Unreleased]` in `CHANGELOG.md`. Keep commits focused. Pull requests should explain the problem and solution, list verification performed, link relevant issues, and include screenshots for visible UI changes. Never commit secrets; provide redacted examples such as `.env.example` for required configuration.
