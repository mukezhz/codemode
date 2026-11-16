# 0008 — Capabilities come from sources; guarantees live in the core

**Status:** accepted
**Date:** 2026-11-16

## Context

Protobuf read as the core of codemode. It never was: `capability`, `codemode`,
`agent`, `llm`, `sandbox` and `program` import neither protobuf nor `registry/`,
and `capability/mcpsource` was already a second source. But every integration
looked like protobuf — buf config, a vendored `codemode.proto` — followed by a
hand-written `switch rpc` with request structs duplicating the schema and its
own JSON decoding. Nothing validated a program's arguments before the handler,
approval was all-or-nothing (`Options.Plan`), and the answer was `any`.

BAML was proposed as a replacement because of Schema-Aligned Parsing: an LLM
result is your type or an error. Its half that matters here is the guarantee.
Its other half, repairing malformed JSON, does not apply: a program's values
leave the interpreter as exact JSON. BAML also has no notion of a service, no
place for roles or approval, no runtime descriptors to read a registry from, and
a Rust runtime that would break `CGO_ENABLED=0`.

## Decision

A source is an adapter that produces `codemode.Source{Registry, Execute}`.
`gosource` (typed Go functions), `registry` (protobuf) and `mcpsource` (MCP
tools) are sources; `codemode.Combine` serves several as one. The developer
default is `gosource`: the structs are the schema and there is no switch.

The guarantees belong to the core, so every source gets them:

- **Input validation.** `codemode` validates each call against the capability's
  input shape before `ExecuteFunc`. Errors name the path and go back to the
  model to repair. A rejected call is not recorded, so it never counts as a write.
- **Typed answers.** `agent.RunAs[T]` tells the model `T`'s shape, checks the
  return value and decodes it. A mismatch is a repairable failure.
- **Approval.** `Capability.NeedsApproval`, declared by the source. A held write
  holds every later write, so the plan keeps the program's order. Plans live
  server-side in an `agent.PlanStore` and apply once, by ID, for their owner.
- **Fail-closed access.** `gosource.Func` takes access as a required argument
  with no zero value; `mcpsource` still requires a role; protobuf keeps its
  annotations.

`TypeSchema` gains `Required`, honoured only by sources that mean it (Go
structs, JSON Schema; not proto3), and an `integer` kind.

BAML is not the core. If it is added, it is a source: BAML-generated Go types
may already work with `gosource`, which a spike must confirm (cgo, and whether
`@description` survives) before any `bamlsource` package exists.

## Consequences

- A call that used to reach a handler with an unknown field or the wrong type
  now fails first. Hand-built capabilities with no input `Fields` are not
  checked, so existing test doubles keep working.
- Proto registries still send every integer as `number`; protobuf typed
  dispatch, where `protojson` would enforce it, is v0.2.
- A held write returns `{}` to the program, so the steps after it may plan
  wrongly. Documented, not solved.
- MCP clients cannot approve: `run_program` refuses and names the held writes
  until an `apply_plan` tool exists.
