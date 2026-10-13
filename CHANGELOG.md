# Changelog

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). While the major
version is 0, a minor bump may break the API.

## [Unreleased]

### Added
- `subprocess-goja`, `subprocess-starlark`, `subprocess-lua` engines and the
  `codemode-worker` binary: programs run in a child process with a heap cap, an
  empty environment and, on Linux, rlimits and namespaces.
- `sandbox.Options.Worker` and `MaxHeapBytes` (`SANDBOX_WORKER`,
  `SANDBOX_MAX_HEAP_BYTES`).
- `mcpserver`: serves a `CodeMode` to MCP clients as `search_capabilities` and
  `run_program`; `examples/todo/cmd/todo-mcp`.
- `deploy/worker.Dockerfile`, `sandbox.Options.WorkerIsolated`
  (`SANDBOX_WORKER_ISOLATED`) and `make test-container`: subprocess engines can
  run each worker in a locked-down container, optionally under gVisor.
- On Linux, `codemode-worker` installs a seccomp denylist before running a program.
- `examples/taskreport`: per-user chat rate limit and concurrency cap, `-audit`
  JSON-lines log of every capability call, and `SANDBOX_ENGINE` now selects the
  chat sandbox.
- `capability/mcpsource`: import an MCP server's tools as capabilities, restricted
  to a role and treated as writes; tools with imprecise schemas are skipped.
- `SECURITY.md` with the threat model per runner.
- Fuzz tests for the worker protocol and the descriptor parser; govulncheck in CI.

### Changed
- Results from subprocess engines cross JSON, so numbers are `float64`.
