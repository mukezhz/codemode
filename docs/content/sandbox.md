# Sandbox

The sandbox evaluates untrusted, model-authored programs. Which engine does that
is a deployment choice, not a code change.

## The interface

```go
type Runner interface {
    Run(ctx context.Context, mode codemode.CodeMode, source string) (any, error)
    Language() string
    Prompt(matches []capability.Match) string
    Name() string
    Close() error
}
```

`Prompt` is the part that keeps this clean. The engine that will *evaluate* a
program writes the instructions for *producing* it — Lua cannot be prompted with
TypeScript signatures, and Starlark cannot be told to use a `while` loop. So
`agent` calls `runner.Prompt(matches)` and never learns which dialect is
configured.

## Three engines

```sh
SANDBOX_ENGINE=goja        # or starlark, or lua
SANDBOX_TIMEOUT=30s
SANDBOX_MAX_CALLS=50
```

| Engine | Language | Notable |
| --- | --- | --- |
| `goja` | JavaScript | Largest surface, most familiar to a model. The default. |
| `starlark` | A small Python dialect | No `while` loops, no recursion, no ambient I/O — by construction, not by removal. |
| `lua` | Lua 5.1 | Smallest and fastest. Only base, table, string, math. |

All three are pure Go, expose the same single `codemode.execute` binding, and
enforce the same timeout and call budget. Aliases `js`/`javascript` and `python`
also resolve, for whichever name comes to hand.

The same job, three ways:

::: code-group

```js [javascript]
const { todos } = codemode.execute("todos.list", {});
const pending = todos.filter(todo => !todo.completed);
if (pending.length === 0) {
  return "nothing to do";
}
codemode.execute("todos.complete", { id: pending[0].id });
return "completed " + pending[0].title;
```

```python [starlark]
todos = codemode.execute("todos.list", {})["todos"]
pending = [todo for todo in todos if not todo["completed"]]
if len(pending) == 0:
    return "nothing to do"
codemode.execute("todos.complete", {"id": pending[0]["id"]})
return "completed " + pending[0]["title"]
```

```lua [lua]
local todos = codemode.execute("todos.list", {}).todos
local pending = {}
for _, todo in ipairs(todos) do
  if not todo.completed then
    pending[#pending + 1] = todo
  end
end
if #pending == 0 then
  return "nothing to do"
end
codemode.execute("todos.complete", { id = pending[1].id })
return "completed " .. pending[1].title
```

:::

## What each engine removes

Every engine starts from an empty global scope and adds back only `codemode`.

**goja** — a fresh interpreter per run. No `require`, no `fetch`, no filesystem,
no `process`. Stopped via `vm.Interrupt`.

**starlark** — the interpreter ships without file, network, or clock builtins, so
there is nothing to remove. The zero `FileOptions` is the restrictive one: no
`while`, no recursion, no top-level control flow. `print` is stubbed out so the
return value is the only output channel. Stopped via `thread.Cancel`.

**lua** — opened with `SkipOpenLibs`, then only base, table, string, and math are
added; `io`, `os`, `package`, and `debug` never exist. `load`, `loadstring`,
`dofile`, `loadfile`, and `print` are deleted afterward. Stopped via
`state.SetContext`.

## Bounds

Two limits are enforced from outside the program, in every engine:

- **`SANDBOX_TIMEOUT`** — wall clock. A runaway program is interrupted.
- **`SANDBOX_MAX_CALLS`** — how many capability calls it may make.

Options passed in code win over the environment, so a caller that sets a bound
explicitly is not overridden by deployment configuration.

```go
runner, err := sandbox.New(sandbox.Options{MaxCalls: 10})
defer runner.Close()

answer, err := runner.Run(ctx, mode, source)
```

`Runner.Name()` reports the engine *and* its bounds, so an operator can see what
is actually enforcing the boundary:

```
goja / JavaScript (in-process, 30s timeout, 50 call budget)
```

## The isolation ladder

::: danger None of these is a security boundary
All three engines run in-process, in your address space. They deny ambient
capabilities and bound time and calls, which is right for code your own host asked
a model to write. They are **not** a defence against a determined attacker.
:::

| Engine | Isolation | Cost |
| --- | --- | --- |
| goja / starlark / lua | Same process, same heap. Bounds and allowlist only | Zero, pure Go |
| Subprocess + JSON lines over stdio | OS process; seccomp and rlimits available | ~60 lines, needs a runtime |
| wasm | Memory-isolated, deterministic | Heavier toolchain |
| Container | Real | Infrastructure |

The bridge shape is identical in all four — `(name, input) → output`. Only the
transport changes, so moving up the ladder is a new file implementing `Runner`,
not a change to `agent`, the host, or any prompt.

A production sandbox for genuinely untrusted programs should additionally enforce:

- A **per-run** capability allowlist. A registered capability is not
  automatically allowed for every user or request.
- No application credentials inside the sandbox. They stay in the host RPC layer.
- CPU, memory, and output-size limits alongside the wall clock.
- A fresh session, or an explicitly scoped one, followed by cleanup.
- Host-side authentication, authorization, rate limits, and audit logs on every
  capability call.

## Adding an engine

Implement `Runner`, then add a case to `sandbox.New`. Concretely:

1. A `dialect` value — its id, how to describe the language to a model, its
   rules, a worked example, a signature renderer, and its comment syntax.
2. `Run` — build an interpreter, bind one `execute` function that counts calls
   and forwards to `mode.Execute`, wire cancellation, convert values both ways.
3. A row in `engines_test.go`, which holds every engine to the same assertions:
   composing calls, rejecting unregistered names, the call budget, terminating a
   runaway, and returning nil for no value.

That test also runs each dialect's own prompt example on its own interpreter — a
prompt that teaches syntax the engine rejects is a bug, and this catches it.
