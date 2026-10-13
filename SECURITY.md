# Security

## Threat model

Code mode runs programs a model wrote. Treat every program as hostile: anyone who
can put text in front of the model (a user's request, a task title, a tool's
output) can steer what it writes.

**Trusted:** the host process, its `ExecuteFunc`, and the protos.
**Untrusted:** the program, and anything running it.

### What each runner guarantees

| Runner | Stops | Does not stop |
| --- | --- | --- |
| `goja`, `starlark`, `lua` (in-process) | ambient access (no files, network, environment); unregistered capabilities; call, time and per-response budgets | a program exhausting host memory; an interpreter escape, which lands in the host process |
| `subprocess-*` on macOS | the above, plus host memory exhaustion (heap cap) and environment leakage | an escape reaching the user's files and network |
| `subprocess-*` on Linux | the above, plus a seccomp filter denying network, exec, file access and namespace syscalls; no network and a private mount namespace where user namespaces are allowed; CPU and fd rlimits | kernel exploits; hosts with unprivileged user namespaces disabled fall back to a plain child process |
| `subprocess-*` with the worker in a container under gVisor | the above, behind a separate kernel | — this is the supported boundary for multi-tenant input |

In every runner the registry check, roles, call budget and response limits run in
the host, outside the program's reach.

### Always the host's job

- **Row scoping.** `ExecuteFunc` decides whose data a call sees. Never take a
  tenant or owner from the program's input.
- **Roles in protos.** Restrict RPCs with `(codemode.roles)` /
  `(codemode.default_roles)` and build `mode` from `registry.ForRole(role)`.
- **Credentials stay in the host.** The bridge carries `(name, input) → output`
  and nothing else.
- **Rate limits and audit** per user, via `agent.Options.OnCall`.

## Reporting a vulnerability

Do not open a public issue. Use GitHub's private vulnerability reporting on this
repository (Security → Report a vulnerability). Expect an acknowledgement within
five working days.
