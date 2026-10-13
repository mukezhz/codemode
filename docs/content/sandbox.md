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
runner, err := sandbox.New(sandbox.Options{Engine: sandbox.EngineGoja, MaxCalls: 10})
defer runner.Close()

answer, err := runner.Run(ctx, mode, source)
```

`Runner.Name()` reports the engine *and* its bounds, so an operator can see what
is actually enforcing the boundary:

```
goja / JavaScript (in-process, 30s timeout, 50 call budget)
```

## The isolation ladder

::: danger The in-process engines are not a security boundary
goja, starlark and lua run in your address space. They deny ambient
capabilities and bound time and calls, which is right for code your own host asked
a model to write. They are **not** a defence against a determined attacker, and a
program that allocates without limit can exhaust the host's memory.
:::

| Engine | Isolation | Cost |
| --- | --- | --- |
| goja / starlark / lua | Same process, same heap. Bounds and allowlist only | Zero, pure Go |
| `SANDBOX_ISOLATION=process` | Child process: own heap, empty environment, heap cap; on Linux no network and a private mount namespace | A process spawn per run, a `codemode-worker` binary |
| wasm | Memory-isolated, deterministic | Heavier toolchain |
| Container | Real | Infrastructure |

### Subprocess engines

Step-by-step setup, every option, and troubleshooting: [Running the worker](/worker).

Each run starts `codemode-worker`, which runs the same interpreter in a child
process and sends every `codemode.execute` back to the host as a JSON line. The
registry check, roles, call budget, response limit and timeout all stay in the
host, so a program that escapes its interpreter still has nothing to call but
the bridge.

```sh
go get -tool github.com/mukezhz/codemode/cmd/codemode-worker   # in your module, once
go install github.com/mukezhz/codemode/cmd/codemode-worker
SANDBOX_ENGINE=goja
SANDBOX_ISOLATION=process
SANDBOX_WORKER=codemode-worker      # argv; defaults to codemode-worker on PATH
SANDBOX_MAX_HEAP_BYTES=268435456    # worker exits when its heap passes this
```

```go
import (
    _ "github.com/mukezhz/codemode/sandbox/goja"       // for the prompt
    _ "github.com/mukezhz/codemode/sandbox/subprocess" // for the process
)
```

Programs and prompts are unchanged; only results differ in one way: they cross
JSON, so numbers come back as `float64`.

On Linux the worker also runs in new user, network and mount namespaces, and
under `RLIMIT_CPU` and `RLIMIT_NOFILE`, and installs a seccomp filter before
running the program: sockets, `exec`, file opens and writes, `ptrace`, mounts,
namespaces, `bpf` and module loading all fail with `EPERM`. It is a denylist,
so a Go upgrade cannot break the worker; on a kernel without seccomp the worker
says so on stderr and runs without it. Where unprivileged user
namespaces are disabled, the runner falls back to a plain child process.

### Container workers

Against a hostile tenant, run the worker in a container. The protocol and the
host's limits are the same; only the argv changes.

```sh
docker build -f deploy/worker.Dockerfile -t codemode-worker .
SANDBOX_ENGINE=goja
SANDBOX_ISOLATION=process
SANDBOX_WORKER_CONTAINER=true
SANDBOX_WORKER="docker run -i --rm --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 65534:65534 \
  --memory 256m --memory-swap 256m --cpus 0.5 --pids-limit 32 \
  --runtime runsc codemode-worker"
```

- `SANDBOX_WORKER_CONTAINER=true` stops the host unsharing namespaces around the
  `docker` CLI, which would cut it off from the Docker socket. The container
  provides the isolation instead.
- `--runtime runsc` is gVisor: a separate kernel between the program and yours.
  Without it the container shares the host kernel.
- The image is `codemode-worker` on `scratch`: no shell, no libc.
- Each run starts a fresh container (about 150–400 ms) and nothing is reused.
  The worker exits on its own one second after its timeout, so a killed CLI
  cannot leave it running.

`make test-container` runs the engine suite this way (`RUNTIME=runsc` to add gVisor).

The bridge shape is identical in all four — `(name, input) → output`. Only the
transport changes, so moving up the ladder is a package that registers a
`sandbox.Isolation` from `init` (as `sandbox/subprocess` does for `process`), not
a change to `agent`, the host, or any prompt.

A production sandbox for genuinely untrusted programs should additionally enforce:

- A **per-run** capability allowlist. A registered capability is not
  automatically allowed for every user or request.
- No application credentials inside the sandbox. They stay in the host RPC layer.
- CPU, memory, and output-size limits alongside the wall clock.
- A fresh session, or an explicitly scoped one, followed by cleanup.
- Host-side authentication, authorization, rate limits, and audit logs on every
  capability call.

## Adding an engine

Implement `Runner`, then register it from your package's `init`. Concretely:

1. A `dialect` value — its id, how to describe the language to a model, its
   rules, a worked example, a signature renderer, and its comment syntax.
2. `Run` — build an interpreter, bind one `execute` function that counts calls
   and forwards to `mode.Execute`, wire cancellation, convert values both ways.
3. A row in `engines_test.go`, which holds every engine to the same assertions:
   composing calls, rejecting unregistered names, the call budget, terminating a
   runaway, and returning nil for no value.

That test also runs each dialect's own prompt example on its own interpreter — a
prompt that teaches syntax the engine rejects is a bug, and this catches it.
