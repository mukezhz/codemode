# Repository Guidelines

## Project Structure & Module Organization

Protobuf contracts live in `proto/`. The `registry/` command converts a Buf descriptor set into generated `registry.json`. Shared registry types and SQLite Vector search live in `capability/`; tests sit beside their Go source. Keep generated descriptor and registry files out of commits. Root configuration includes `buf.yaml`, `mise.toml`, and the `Makefile`.

## Build, Test, and Development Commands

Install pinned tools with `mise install`, then use:

- `make generate` — build protobuf descriptors with Buf and regenerate `registry/registry.json`.
- `make test` — run Buf lint and all Go tests; the native vector test skips without its extension.
- `SQLITE_VECTOR_EXTENSION=/absolute/path/vector.dylib make test-vector` — exercise real semantic search using sqlite-vector 1.0.0. Use `vector.so` on Linux.

Each command should work from the repository root and fail with a nonzero exit code on errors.

## Coding Style & Naming Conventions

Format Go with `gofmt` and protobuf with Buf. Use tabs as produced by `gofmt`; exported Go names use `PascalCase`, internal names use `camelCase`, and package names remain short and lowercase. Protobuf messages use `PascalCase` and fields use `snake_case`. Reuse shared definitions from `capability/schema.go`.

## Testing Guidelines

Use Go's standard `testing` package and name tests `TestBehavior`. Keep unit tests deterministic. Native sqlite-vector tests must read the library path from `SQLITE_VECTOR_EXTENSION`; never search for or load arbitrary extensions. Add one focused regression test for each non-trivial branch or bug fix. No coverage threshold is configured.

## Commit & Pull Request Guidelines

There is no Git history from which to infer a commit convention. Until one is established, use concise imperative subjects, for example `Add input validation`. Keep commits focused. Pull requests should explain the problem and solution, list verification performed, link relevant issues, and include screenshots for visible UI changes. Never commit secrets; provide redacted examples such as `.env.example` for required configuration.
