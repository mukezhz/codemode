# Changelog

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). While the major
version is 0, a minor bump may break the API.

## [Unreleased]

### Added
- `(codemode.permissions)` and `(codemode.default_permissions)` beside roles, for
  applications whose roles are defined at runtime: the proto names the
  permission an RPC needs, the application decides which roles grant it.
  A method that declares roles or permissions replaces both service defaults.
- `capability.Registry.For(capability.Caller{Roles, Permissions})`: the
  capabilities a caller may call when they hold any listed role or permission,
  plus every unrestricted one. Build the caller per request from your app.
- `capability.Registry.Permissions()`: every permission the protos define, for a
  role-editing screen.
- Permissions are `resource.action`, with the action one of `read`, `write`,
  `delete` or `manage` (`capability.PermissionPattern`); anything else fails
  registry loading. Controls of their own are sub-resources
  (`deals.discounts.write`). `Registry.ValidatePermissions` checks names an admin
  is about to grant against the pattern and the protos.
- Permission actions: `write` also grants `read` on its resource, `manage`
  grants every action on its resource and sub-resources, `delete` grants only
  itself (`capability.Grants`). `Registry.Permissions()` offers `resource.manage`.
- `registry.MarshalJSON`: encodes a proto response in the shape the registry
  describes (proto field names, empty lists as `[]`, 64-bit integers as numbers).
  Plain `protojson.Marshal` disagrees with the schema on all three.
- `capability.Keyword`: keyword search over a registry with no database, so a
  first integration needs no search code of its own.
- `SANDBOX_ISOLATION=process` (`sandbox.Options.Isolation`) and the
  `codemode-worker` binary: programs run in a child process with a heap cap, an
  empty environment and, on Linux, rlimits and namespaces.
- `sandbox.Options.Worker` and `MaxHeapBytes` (`SANDBOX_WORKER`,
  `SANDBOX_MAX_HEAP_BYTES`).
- `mcpserver`: serves a `CodeMode` to MCP clients as `search_capabilities` and
  `run_program`; `examples/todo/cmd/todo-mcp`.
- `deploy/worker.Dockerfile`, `sandbox.Options.WorkerContainer`
  (`SANDBOX_WORKER_CONTAINER`) and `make test-container`: subprocess engines can
  run each worker in a locked-down container, optionally under gVisor.
- `sandbox.New` rejects likely misconfigurations (worker settings with in-process
  isolation, an unknown isolation, a container flag without a command) and, for
  `process`, runs one program through the worker so a broken worker fails at startup.
- On Linux, `codemode-worker` installs a seccomp denylist before running a program.
- `examples/taskreport`: per-user chat rate limit and concurrency cap, `-audit`
  JSON-lines log of every capability call, and `SANDBOX_ENGINE` now selects the
  chat sandbox.
- `capability/mcpsource`: import an MCP server's tools as capabilities, restricted
  to a role and treated as writes; tools with imprecise schemas are skipped.
- `SECURITY.md` with the threat model per runner.
- Fuzz tests for the worker protocol and the descriptor parser; govulncheck in CI.

### Fixed
- `CodeMode.Search` drops matches outside its registry and returns the
  registry's copy of each, so a search index shared across roles or tenants no
  longer tells the model about capabilities it cannot call.

### Changed
- Results from subprocess engines cross JSON, so numbers are `float64`.
