# 0006 — MCP server: two generic tools, client writes the program

**Status:** accepted
**Date:** 2026-10-13

## Context

MCP clients already own conversation, delegation and distribution. Exposing
codemode to them is cheaper than competing with them.

## Decision

`mcpserver` registers exactly two tools, `search_capabilities` and `run_program`,
and the client's model writes the program.

- **Not one tool per RPC.** That is the pattern code mode exists to replace: every
  schema in context and every intermediate result through the model.
- **Not `ask(question)` running `agent.Run` server-side.** The client already has a
  capable model; a second one adds latency, cost and API keys. It can be added as
  a third tool if a client without a strong model needs it.
- **No generic binary.** The server needs the host's `ExecuteFunc`. The package is
  the product; `examples/todo/cmd/todo-mcp` shows the wiring.
- **The official SDK** (`github.com/modelcontextprotocol/go-sdk`, v1.8), linked
  only by `mcpserver`. Its typed `AddTool` validates input against the inferred
  schema and turns handler errors into `isError` results, which is what lets the
  client's model retry.
- **Package name `mcpserver`**, not `mcp`, so it does not collide with the SDK's
  package at every call site.
