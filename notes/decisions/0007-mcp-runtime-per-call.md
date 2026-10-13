# 0007 — MCP server builds the caller's runtime on every tool call

**Status:** accepted · extends [0006](0006-mcp-server-shape.md)
**Date:** 2026-10-13

## Context

`mcpserver.New` took one `CodeMode`, so its registry, and therefore the caller's
roles and permissions, were fixed for the server's lifetime. That fits a stdio
server one person launches. It does not fit one HTTP server shared by many users
of an application whose roles are created at runtime and edited by admins
(asterconsult: per-tenant, per-branch roles in a database).

## Decision

`New` takes a `ModeFunc(ctx, *mcp.CallToolRequest) (CodeMode, error)`, called on
every tool call. The application reads the verified token or headers from
`request.Extra`, fetches that user's roles and permissions, and returns a runtime
built from `registry.For(caller)`. `Static(mode)` keeps the one-user case one line.

- **Per call, not per session.** A role edited mid-session applies on the next
  call; nothing about the caller is cached in the server.
- **Authentication stays in HTTP middleware** (`auth.RequireBearerToken`), so an
  unauthenticated request never reaches a tool. `ModeFunc` only decides what an
  authenticated caller may see and do.
- **An error from `ModeFunc` is a tool error.** Unknown or revoked users fail
  closed and nothing runs.
