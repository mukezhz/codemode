# Development and production

Build one binary and configure it differently. The code that runs a request is
the same on a laptop and in production; what changes is where programs run,
how capabilities are searched, and how many limits sit around them. All of it
is environment, so promoting a build is a configuration change, not a code
change.

| Concern | Development | Production |
| --- | --- | --- |
| Sandbox | `goja` in your process | `goja` in a worker process (`SANDBOX_ISOLATION=process`), or a container for untrusted tenants |
| Worker binary | not needed | `codemode-worker` installed beside your app |
| Search | keyword ranking, no database | semantic index in SQLite or Postgres |
| Program cache | in memory | in memory per replica, or shared through a store |
| Limits | defaults (30 s, 50 calls) | set to your workload |
| Who writes prompts | you | your users, so treat every program as hostile |
| Audit | `-log debug` | one line per capability call through `OnCall` |

## One `main.go` for both

Blank-import every backend either environment might choose. A blank import costs
binary size, nothing at runtime; the environment decides which one is built.

```go
package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/program"
	"github.com/mukezhz/codemode/registry"
	"github.com/mukezhz/codemode/sandbox"

	_ "github.com/mukezhz/codemode/capability/pgstore" // pure Go; sqlitestore needs cgo
	_ "github.com/mukezhz/codemode/llm/claude"
	_ "github.com/mukezhz/codemode/sandbox/goja"       // the language, in both environments
	_ "github.com/mukezhz/codemode/sandbox/subprocess" // the worker, in production

	tasksv1 "example.com/app/gen/tasks/v1"
)

func main() {
	ctx := context.Background()

	full, err := registry.FromFiles(tasksv1.File_tasks_v1_tasks_proto)
	if err != nil {
		log.Fatal(err)
	}
	model, err := llm.NewFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	runner, err := sandbox.NewFromEnv(sandbox.Options{})
	if err != nil {
		log.Fatal(err) // e.g. process isolation chosen but codemode-worker not installed
	}
	defer runner.Close()
	search := keywordOrIndex(ctx, full)
	cache := program.NewExactCache()

	// Say what is enforcing the boundary, so a misconfigured deploy is visible in the first log line.
	slog.Info("codemode", "sandbox", runner.Name(), "model", model.Name())

	http.HandleFunc("POST /ask", func(writer http.ResponseWriter, request *http.Request) {
		user, tenant, caller := identify(request) // your auth: a capability.Caller fetched per request
		mode, err := codemode.New(full.For(caller), search, executeFor(user))
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 2*time.Minute)
		defer cancel()
		result, err := agent.Run(ctx, mode, model, request.FormValue("q"), agent.Options{
			Runner:      runner,
			Cache:       cache,
			CacheCaller: tenant + "|" + strings.Join(caller.Roles, ","),
			OnCall: func(call codemode.Call) {
				slog.Info("capability", "user", user, "roles", caller.Roles, "name", call.Name,
					"ms", call.Duration.Milliseconds(), "error", call.Error)
			},
		})
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadGateway)
			return
		}
		writer.Write([]byte(agent.Answer(result.Answer)))
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// keywordOrIndex uses the semantic index when CAPABILITY_DB_DSN is set, and keyword ranking otherwise.
func keywordOrIndex(ctx context.Context, full capability.Registry) codemode.SearchFunc {
	store, err := capability.OpenStoreFromEnv(ctx)
	if errors.Is(err, capability.ErrNoStore) {
		return capability.Keyword(full)
	}
	if err != nil {
		log.Fatal(err)
	}
	return semanticSearch(ctx, store, full)
}
```

`identify`, `executeFor` and `semanticSearch` are yours. The [Integration guide](/using-it) covers `ExecuteFunc` and both kinds of
search. The point here is that nothing in this file says "development" or
"production".

Two rules make that hold:

- **Build per request what depends on the caller.** `mode` comes from
  `full.For(caller)` and an `ExecuteFunc` bound to the user, so roles and
  row scope can never leak between requests, and a role an admin edits applies
  on the next one.
- **Build once what does not.** The registry, model, runner, search and cache are
  safe for concurrent use and expensive to make.

## Development

```sh
ANTHROPIC_API_KEY=sk-...
# SANDBOX_ENGINE unset: goja, in-process
# CAPABILITY_DB_DSN unset: keyword search, no database
go run .
```

What you get, and why it is the right default here:

- **No worker, no Docker.** Programs run in your process, so a breakpoint in
  `ExecuteFunc` stops mid-program and a panic shows a normal stack trace.
- **No database.** Keyword ranking is fine for a few dozen capabilities.
- **Fast iteration.** A changed proto is a `buf generate` and a restart; the
  registry is read from the generated code at startup.

Useful while building:

| Want | Do |
| --- | --- |
| See the prompt, the program and every call | log `result.Source`; in the examples, `-log debug` |
| Try without an API key | `make demo-todo` runs a fixed program end to end |
| Drive it from Claude Code or Cursor | `make mcp-todo`, or serve your own `mode` with [`mcpserver`](/mcp) |
| Check the production sandbox locally | `make worker`, then `SANDBOX_ISOLATION=process SANDBOX_WORKER=$PWD/bin/codemode-worker` |

Run with `SANDBOX_ISOLATION=process` locally at least once before shipping. The program
and the prompt are identical, but results cross JSON in that mode, so an
integer comes back as a `float64`. That is the one behaviour difference.

## Production

Pick the rung that matches who can influence the prompt. Anyone who can put text
in front of the model (a request, a task title, a document it reads) can steer
the program it writes.

### Internal users you trust

In-process is acceptable. Set the limits deliberately rather than inheriting
demo numbers:

```sh
SANDBOX_ENGINE=goja
SANDBOX_TIMEOUT=10s
SANDBOX_MAX_CALLS=25
```

### End users, one organisation

Run every program in a worker process ([Running the worker](/worker) has every
step and option). A program that allocates without limit
kills its own worker, not your server, and the worker has no environment, so
API keys and database URLs never reach it.

```sh
SANDBOX_ENGINE=goja
SANDBOX_ISOLATION=process
SANDBOX_WORKER=/usr/local/bin/codemode-worker
SANDBOX_MAX_HEAP_BYTES=268435456
SANDBOX_TIMEOUT=10s
SANDBOX_MAX_CALLS=25
```

Ship the worker in the same image as your app. Run
`go get -tool github.com/mukezhz/codemode/cmd/codemode-worker` once and commit
`go.mod`, so the build below uses your codemode version:

```dockerfile
FROM golang:1.27 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /app . && \
    CGO_ENABLED=0 go build -o /go/bin/codemode-worker github.com/mukezhz/codemode/cmd/codemode-worker

FROM gcr.io/distroless/static
COPY --from=build /app /app
COPY --from=build /go/bin/codemode-worker /usr/local/bin/codemode-worker
ENTRYPOINT ["/app"]
```

Pin the worker to the same codemode version as your `go.mod`; the protocol is
versioned, and a mismatch fails every run with a clear error rather than
misbehaving. Release binaries are also attached to each GitHub release.

On Linux the worker also runs under a seccomp filter and CPU and file limits,
and in fresh user, network and mount namespaces where the kernel allows
unprivileged ones (many container platforms do not; it falls back quietly).

### Many tenants, or users you do not trust

Put each worker in its own container, under gVisor where you can. Roles, tenant
scoping and per-tenant policy are in [Roles and tenants](/tenants).

```sh
SANDBOX_ENGINE=goja
SANDBOX_ISOLATION=process
SANDBOX_WORKER_CONTAINER=true
SANDBOX_WORKER="docker run -i --rm --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 65534:65534 \
  --memory 256m --memory-swap 256m --cpus 0.5 --pids-limit 32 \
  --runtime runsc ghcr.io/you/codemode-worker"
```

- Build the image from `deploy/worker.Dockerfile`.
- `SANDBOX_WORKER_CONTAINER=true` is required: without it the host wraps the
  `docker` CLI in namespaces that cut it off from the Docker socket.
- Each run pays a container start (150–400 ms). The program cache skips the
  model call for repeats, not the sandbox.
- Your app needs access to a container runtime. Giving an app container the
  Docker socket hands it root on the node, so run the app on a VM or node you
  treat accordingly, or point `SANDBOX_WORKER` at a runtime CLI that does not
  need one (`podman`, `nerdctl`).

Around the sandbox, the host still owns:

- **Row scoping.** Bind the tenant and user into `ExecuteFunc`; never accept
  them from the program's input.
- **Fairness.** A per-user rate limit and a cap on programs running at once,
  before `agent.Run`.
- **Audit.** One line per call from `OnCall`: user, role, capability, a hash of
  the input.
- **Read-only where possible.** A reporting product can drop writes from the
  registry entirely: `full.Select(func(c capability.Capability) bool { return c.ReadOnly })`.

[`examples/taskreport`](https://github.com/mukezhz/codemode/tree/main/examples/taskreport)
does the first three; see [Many services, many users](/scaling-up#many-tenants).

### Search and cache at scale

```sh
CAPABILITY_DB=postgres          # required: a DSN alone means sqlite
CAPABILITY_DB_DSN=postgres://...
```

That matches the `pgstore` import above. To use sqlite-vector instead, import
`capability/sqlitestore`, set `SQLITE_VECTOR_EXTENSION` to the library's path,
and build with `CGO_ENABLED=1` on a base image with libc
(`gcr.io/distroless/base`), since the driver loads a native extension.

Rebuild the index when the registry changes (at deploy, or on startup when it is
small), not per request. With several replicas, either accept a per-replica
in-memory program cache or use a shared `program.Store`; see
[Caching programs](/caching).

## Before you ship

Start with the [checklist in the Integration guide](/using-it#before-production).
Then, for the environment:

- `runner.Name()` in the first log line says `child process` or `container` (or you decided in-process is right).
- `codemode-worker` is the same version as the library in `go.mod`.
- Timeout, call budget and heap cap are set, not defaulted.
- `OnCall` writes somewhere you will look.
- The handler deadline is well above `SANDBOX_TIMEOUT`, so a slow program fails as a sandbox timeout, not a dropped connection.

Every variable is listed in [Configuration](/configuration); the threat model per
runner is in [`SECURITY.md`](https://github.com/mukezhz/codemode/blob/main/SECURITY.md).
