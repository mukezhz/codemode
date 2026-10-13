# Running the worker

`codemode-worker` is a small binary that runs one program and exits. The
`subprocess-*` engines start one per run, so a program executes in its own
process instead of inside your application. This page is how to install it,
configure it, check it is working, and run it in a container.

You do not need it for development. Leave `SANDBOX_ENGINE` unset and programs
run in-process with goja; see [Development and production](/environments).

## What changes when you turn it on

```
in-process (goja)                         subprocess (subprocess-goja)

your app                                  your app                         codemode-worker
 ├─ agent.Run                              ├─ agent.Run                      └─ goja
 ├─ goja ← program runs here               ├─ registry, roles, budgets   ◄──►   program runs here
 └─ ExecuteFunc                            └─ ExecuteFunc                 JSON lines on stdin/stdout
```

| | In-process | Worker |
| --- | --- | --- |
| Language, prompt, programs | goja / Starlark / Lua | **the same** |
| A program that allocates forever | exhausts your app's memory | the worker exits; your app returns an error |
| Your environment (API keys, DB URLs) | visible to the process the program runs in | the worker gets an empty environment |
| Registry check, roles, call budget, response limit, timeout | in your app | **still in your app** |
| Debugging with breakpoints | in `ExecuteFunc` and the engine | in `ExecuteFunc` only |
| Cost per run | none | a process start (milliseconds) |
| Numbers in the result | `int64` or `float64` | always `float64` (results cross JSON) |

The important row is the fifth. Everything that decides what a program may do
stays in your application. The worker only interprets; every capability call it
makes is a message your application checks before anything runs.

## Step 1: install it

Pick one.

```sh
# From source, matching your go.mod version of codemode
go install github.com/mukezhz/codemode/cmd/codemode-worker@v0.1.0

# Inside this repository
make worker            # → bin/codemode-worker

# A release binary
curl -L -o codemode-worker \
  https://github.com/mukezhz/codemode/releases/download/v0.1.0/codemode-worker-linux-amd64
chmod +x codemode-worker
```

Keep the worker on the **same codemode version** as your application. The two
speak a versioned protocol; a mismatch fails every run with
`protocol version N, worker speaks M` rather than misbehaving.

In a container image, build both in one stage so the versions cannot drift:

```dockerfile
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/app . && \
    CGO_ENABLED=0 go build -o /out/codemode-worker github.com/mukezhz/codemode/cmd/codemode-worker

FROM gcr.io/distroless/static
COPY --from=build /out/app /app
COPY --from=build /out/codemode-worker /usr/local/bin/codemode-worker
ENTRYPOINT ["/app"]
```

`go build github.com/mukezhz/codemode/cmd/codemode-worker` uses the version in
your `go.mod`, which is what keeps them matched.

## Step 2: import the engine

Blank-import the subprocess engine **and** the language engine. The language
engine is still needed in your app: it writes the prompt.

```go
import (
	_ "github.com/mukezhz/codemode/sandbox/goja"       // prompt and in-process runs
	_ "github.com/mukezhz/codemode/sandbox/subprocess" // subprocess-goja, -starlark, -lua
)
```

Importing both costs nothing at runtime. Configuration chooses which one runs,
so the same binary serves development and production.

## Step 3: configure it

From the environment, with `sandbox.NewFromEnv`:

```sh
SANDBOX_ENGINE=subprocess-goja                  # or subprocess-starlark, subprocess-lua
SANDBOX_WORKER=/usr/local/bin/codemode-worker   # optional when it is on PATH
SANDBOX_MAX_HEAP_BYTES=268435456                # 256 MiB, the default
SANDBOX_TIMEOUT=10s
SANDBOX_MAX_CALLS=25
```

```go
runner, err := sandbox.NewFromEnv(sandbox.Options{})
```

Or in code, which wins over the environment:

```go
runner, err := sandbox.New(sandbox.Options{
	Engine:       "subprocess-goja",
	Worker:       []string{"/usr/local/bin/codemode-worker"},
	MaxHeapBytes: 256 << 20,
	Timeout:      10 * time.Second,
	MaxCalls:     25,
})
defer runner.Close()
```

| Option | Env | Default | Meaning |
| --- | --- | --- | --- |
| `Engine` | `SANDBOX_ENGINE` | `goja` | `subprocess-` prefix runs the worker |
| `Worker` | `SANDBOX_WORKER` | `codemode-worker` on `PATH` | argv that starts a worker; split on spaces from the env |
| `WorkerIsolated` | `SANDBOX_WORKER_ISOLATED` | `false` | `true` when `Worker` starts a container (step 5) |
| `MaxHeapBytes` | `SANDBOX_MAX_HEAP_BYTES` | 256 MiB | worker exits when its heap passes this |
| `Timeout` | `SANDBOX_TIMEOUT` | 30 s | whole program, enforced by your app |
| `MaxCalls` | `SANDBOX_MAX_CALLS` | 50 | capability calls per program, counted by your app |
| `MaxResponseBytes` | — | 1 MiB | each capability response, and the final value |

Nothing else changes: `agent.Run`, `mcpserver.New` and `runner.Run` take the
runner exactly as before.

## Step 4: check it is working

Log the runner's name at startup. It states what is enforcing the boundary:

```go
slog.Info("sandbox", "runner", runner.Name())
```

```
subprocess-goja / javascript (child process, 10s timeout, 25 call budget, 256 MiB heap)
```

If it says `in-process`, the environment did not reach the binary.

Then run one program by hand, with no application involved:

```sh
echo '{"v":1,"type":"run","engine":"goja","source":"return 1+1","limits":{"timeout_ms":1000,"max_calls":1,"max_response_bytes":1024,"max_heap_bytes":67108864}}' \
  | codemode-worker
{"type":"done","value":2}
```

And check that a runaway program costs only the worker:

```go
_, err := runner.Run(ctx, mode, `const a = []; while (true) a.push(new Array(1e6).fill(1));`)
// err: sandbox: memory limit of 268435456 bytes exceeded
```

## What the worker does on each platform

| | macOS | Linux | Linux in a container |
| --- | --- | --- | --- |
| Own process, empty environment, empty working directory | ✓ | ✓ | ✓ |
| Heap cap (`MaxHeapBytes`) | ✓ | ✓ | ✓ plus `--memory` |
| Killed with its whole process group on timeout | ✓ | ✓ | the container exits on its own |
| CPU and open-file limits | — | ✓ | ✓ |
| Seccomp: no sockets, `exec`, file opens, `ptrace`, mounts | — | ✓ | ✓ |
| No network, private mounts (user namespaces) | — | where allowed | ✓ `--network none` |
| Separate kernel | — | — | with `--runtime runsc` (gVisor) |

"Where allowed": many kernels and container platforms disable unprivileged user
namespaces. The worker then runs without them rather than failing, and the
other rows still hold.

## Step 5: a container per run

For tenants you do not trust, start each worker in its own container. Same
protocol, same configuration, different argv.

Build the worker image (`scratch`: the binary and nothing else):

```sh
docker build -f deploy/worker.Dockerfile -t ghcr.io/you/codemode-worker:v0.1.0 .
```

Point the runner at it:

```sh
SANDBOX_ENGINE=subprocess-goja
SANDBOX_WORKER_ISOLATED=true
SANDBOX_WORKER="docker run -i --rm --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 65534:65534 \
  --memory 256m --memory-swap 256m --cpus 0.5 --pids-limit 32 \
  --runtime runsc ghcr.io/you/codemode-worker:v0.1.0"
```

| Flag | Why |
| --- | --- |
| `-i` | the protocol is stdin/stdout |
| `--rm` | one run, one container, nothing reused |
| `--network none`, `--read-only` | nothing to reach, nothing to write |
| `--cap-drop ALL`, `no-new-privileges`, `--user 65534` | no privileges to escalate from |
| `--memory`, `--memory-swap` | the hard memory cap behind `MaxHeapBytes` |
| `--cpus`, `--pids-limit` | one program cannot starve the node |
| `--runtime runsc` | gVisor: a separate kernel; omit only where it is not installed |

`SANDBOX_WORKER_ISOLATED=true` is required. Without it your app wraps the `docker`
command in new namespaces, which cuts it off from the Docker socket.

Each run pays a container start, roughly 150–400 ms. A worker that outlives its
timeout (because the `docker` client was killed) exits on its own one second
later.

::: warning The Docker socket is root
Your application needs to start containers. Mounting `/var/run/docker.sock` into
an application container gives that container root on the node. Run the
application on a VM or node you treat as privileged, or set `SANDBOX_WORKER` to a
runtime that does not need a root daemon (`podman run ...`, `nerdctl run ...`).
:::

Test it the way CI does:

```sh
make test-container                # the whole engine suite, each run in a container
make test-container RUNTIME=runsc  # under gVisor
```

## Troubleshooting

| Error | Cause | Fix |
| --- | --- | --- |
| `subprocess engine needs codemode-worker: set Options.Worker or install it on PATH` | worker not found | install it, or set `SANDBOX_WORKER` to its path |
| `sandbox engine "goja" is not registered: add _ ".../sandbox/goja"` | only the subprocess engine imported | blank-import the language engine too (step 2) |
| `protocol version 1, worker speaks 2` | app and worker from different codemode versions | build both from the same `go.mod` |
| `sandbox: memory limit of N bytes exceeded` | the program allocated past `MaxHeapBytes` | the program's fault; raise the cap only if real data needs it |
| `sandbox stopped: context deadline exceeded` | the program ran past `Timeout` | same; it is a runaway or the timeout is too tight |
| `worker protocol error: ...` | the worker sent something malformed or died mid-run | the stderr tail is attached; a crash here is a bug worth reporting |
| `worker protocol error: EOF` with `permission denied ... docker` in the stderr tail | the `docker` CLI was wrapped in namespaces, or the app cannot reach the daemon | set `SANDBOX_WORKER_ISOLATED=true`; check the app's access to the runtime |
| stderr: `seccomp unavailable, running without it` | kernel or emulator cannot load filters | expected under emulation; on a real host, check the kernel config |

## How it works, briefly

The worker reads one `run` message with the source and limits, applies its own
limits, then runs the same engine your app would have run in-process. That
engine is given a `CodeMode` whose `Execute` writes a `call` line and waits for
the `result` line. Your app answers each `call` with its real `CodeMode`, so the
registry, roles, `Recorder` and `OnCall` all work unchanged. A `done` or `error`
line ends the run, and your app kills the worker's process group either way.

Your app trusts nothing the worker says. It counts calls itself, checks every
message's type and id, caps every line's size, and enforces the timeout from
outside. A program that escapes the interpreter controls the worker, and the
worker still has nothing to call but your app's checked bridge.

Design record: `notes/decisions/0005-subprocess-runner.md`.
