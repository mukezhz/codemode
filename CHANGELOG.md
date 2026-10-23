# Changelog

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). While the major
version is 0, a minor bump may break the API.

## [Unreleased]

### Added
- `capability.TypeSchema` carries `descriptions`, `optional`, `oneofs`, and, on
  an enum, its `values`, and every dialect's prompt renders them. Protos fill
  them from `(codemode.field_description)`, a new field option, proto3
  `optional`, `oneof` and `enum`; `mcpsource` fills them from JSON Schema
  `description`, `required` and string `enum`.
- Well-known types are described in their JSON form: `Timestamp` and `Duration`
  as strings with their format, wrappers as their optional scalar, `Struct` and
  `Value` as `any`. `registry.MarshalJSON` sends `Int64Value` as a number to
  match.
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
- `mcpserver`: serves codemode to MCP clients as `search_capabilities` and
  `run_program`. `New` takes a `ModeFunc` that builds the caller's runtime on
  every tool call, so one HTTP server can serve many users with their own,
  current permissions; `Static(mode)` serves one stdio user.
  `examples/todo/cmd/todo-mcp`.
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
- The agent no longer caches a program that writes, planned a write, or names a
  write capability it was shown: a semantic hit for a similar request would have
  rerun it with the old request's values.
- The semantic program cache only reuses a program when the numbers and quoted
  text in both requests match, so "complete task 13" never reuses the program
  for "complete task 12".
- `agent.Options.Instructions` is part of the cache key (`program.Key.Instructions`),
  so a program written for one user's instructions is not served to another.
  Existing cache entries miss once and are rewritten.
- MCP `run_program` returns the calls a failed program made, and `wrote: true`
  when a write ran before the failure, instead of a bare error telling the
  client to retry.
- `CodeMode.Search` drops matches outside its registry and returns the
  registry's copy of each, so a search index shared across roles or tenants no
  longer tells the model about capabilities it cannot call.

### Changed
- Docs lead with integrating codemode: a five-step summary on the landing page
  and README, the sidebar ordered Get started → Ship it → How it works, and one
  pre-production checklist. Fixed stale snippets, including a Dockerfile that
  could not build with `sqlitestore` and an HTTP handler that shared one `mode`
  across users.
- Results from subprocess engines cross JSON, so numbers are `float64`.
