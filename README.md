# Code Mode

Turn typed Go functions, protobuf RPCs or MCP tools into capabilities a model
composes inside a sandbox.

Ordinary tool use makes a model work one call at a time: fifty tasks, fifty round
trips, every intermediate result back through the model. Code mode has the model
write a **program** instead. The program loops and filters at machine speed
inside a sandbox, and only the final answer returns.

```js
const { todos } = codemode.execute("todos.list", {});
const pending = todos.filter(todo => !todo.completed);
codemode.execute("todos.complete", { id: pending[0].id });
return "completed " + pending[0].title;
```

The sandbox cannot reach your database, network, or secrets. It gets one
function, and that function goes back to Go, which checks a generated registry
before anything runs.

## Use it in your app

```sh
go get github.com/mukezhz/codemode
```

The Go functions you already have are the capabilities; their structs are the
schema:

```go
type ListOrders struct {
	Status string `json:"status,omitempty" desc:"only orders in this status"`
}

// 1. your code: typed functions, the only place that touches data
source, err := gosource.New(
	gosource.Func("orders.list", "Lists orders", gosource.Public(), shop.List, gosource.ReadOnly()),
	gosource.Func("orders.refund", "Refunds a paid order", gosource.Roles("support"), shop.Refund, gosource.NeedsApproval()),
)

// 2. per request: only what this caller may call
mode, err := codemode.FromSource(source, capability.Caller{Roles: user.Roles})

// 3. model writes a program, sandbox runs it, the answer comes back as your struct
model, err := llm.NewFromEnv()                       // ANTHROPIC_API_KEY or OPENAI_API_KEY
runner, err := sandbox.NewFromEnv(sandbox.Options{}) // in-process JS by default
revenue, result, err := agent.RunAs[Revenue](ctx, mode, model, "how much have paid orders brought in?",
	agent.Options{Runner: runner, Plans: plans, PlanOwner: user.ID, OnEvent: agent.LogEvents(logger)})

// 4. a refund waits for a person, in your database, until they decide
var pending *agent.Pending
if errors.As(err, &pending) {
	// send pending.ID and pending.ExpiresAt to the client; later, on approval:
	plan, err := plans.Approve(ctx, mode, user.ID, pending.ID)
}
// every other error is an *agent.Error carrying result.RunID and the stage that failed
```

No codegen and no `switch` over RPC names. Every call the program makes is
validated against your structs before your function runs; a bad call goes back
to the model to fix. Access is required on every capability, so nothing is
exposed by omission.

Already describe your API in protobuf, or have MCP servers? They are
[sources too](docs/content/sources.md), and `codemode.Combine` serves several at
once. Blank-import the backends you want (`llm/claude`, `sandbox/goja`, …);
nothing else is linked. [**Quickstart**](docs/content/quickstart.md) builds this
as complete files in ten minutes.

## Quick start

```sh
mise install
make demo-todo
```

Runs end to end with no API keys and no database: search falls back to keyword
ranking, and a fixed program stands in for the model. Then give a model the pen:

```sh
export ANTHROPIC_API_KEY=...
go run ./examples/todo -request "finish anything about docs"
```

For something complete — a task tracker with a web UI, SQLite storage,
twenty-three capabilities, per-user scoping, and charts rendered from a prompt —
see [`examples/taskreport`](examples/taskreport). Go is the only requirement;
the frontend is built and committed:

```sh
go run ./examples/taskreport -serve :8080 \
  -base-url https://api.deepseek.com/v1 -api-key sk-... -model deepseek-chat
```

Ask it *"what is overdue, and which of it is high priority?"* and one model turn
produces one program, which returns a document the browser renders as stat
cards, a chart, and a table. Works against any OpenAI-compatible endpoint.

Two more show the new pieces in isolation: [`examples/shop`](examples/shop/README.md)
(the quickstart: typed answer, approved refund) and
[`examples/combined`](examples/combined/README.md) (Go functions, a proto service
and an MCP server in one program). Each example has a step-by-step README: [`examples/todo`](examples/todo/README.md)
(no key, no database, also an MCP server for Claude Code) and
[`examples/taskreport`](examples/taskreport/README.md) (web UI, roles, rate
limits, audit log). Both run programs in-process by default; add
`SANDBOX_ISOLATION=process` after `make worker` to run them in `codemode-worker`.

## Documentation

Full docs are a VitePress site under [`docs/`](docs/content).

**Get started**
- [**Quickstart**](docs/content/quickstart.md) — an empty directory to a typed answer and an approved write, in six steps
- [Capability sources](docs/content/sources.md) — Go structs, protobuf, MCP, or several at once · [Capabilities from protobuf](docs/content/protobuf.md)
- [**Integration guide**](docs/content/using-it.md) — every integration choice, explained
- [Adding it to an existing app](docs/content/existing-app.md) — a CRM walked through: contract, adapter, roles, tenants, confirmed writes, and a non-Go CRM via a sidecar

**Ship it**
- [Development and production](docs/content/environments.md) · [Running the worker](docs/content/worker.md) — same binary, different isolation
- [What your binary links](docs/content/dependencies.md) — the blank imports, and why
- [Permissions](docs/content/permissions.md) · [Roles and tenants](docs/content/tenants.md) — roles in the contract, rows in your code
- [Types and validation](docs/content/types.md) · [Approving writes](docs/content/approval.md) · [Observability](docs/content/observability.md)
- [Writes and retries](docs/content/writes.md) · [Caching programs](docs/content/caching.md)

**Grow it**
- [Many services, many users](docs/content/scaling-up.md) · [Serving MCP clients](docs/content/mcp.md)

**How it works**
- [What code mode is](docs/content/overview.md) · [Architecture](docs/content/architecture.md) · [The flow, step by step](docs/content/flow.md)
- [Capabilities](docs/content/capabilities.md) · [Search](docs/content/search.md) · [Models](docs/content/models.md) · [Sandbox](docs/content/sandbox.md)

**Reference**
- [Configuration](docs/content/configuration.md) · [Latency](docs/content/latency.md) · [Troubleshooting](docs/content/troubleshooting.md)

```sh
make docs        # serve locally with hot reload
make docs-build  # build the static site
```

## Layout

| Path | What it is |
| --- | --- |
| `proto/` | `codemode.proto`: the service and method annotations (namespace, description, roles, permissions), embedded for consumers |
| `registry/` | Builds a registry from linked descriptors or a Buf descriptor set; CLI in `cmd/codemode-registry/` |
| `gen/` | Generated Go for `codemode.proto`. Each example generates into its own tree |
| `capability/` | Registry types, validation and the vector index; backends in `sqlitestore/` and `pgstore/` |
| `capability/gosource/` | Capabilities from typed Go functions |
| `capability/mcpsource/` | Capabilities from an MCP server's tools |
| `codemode/` | The two-method public interface, `Search` and `Execute`, and `Source` / `Combine` |
| `llm/` | The `Client` interface; providers in `claude/` and `openai/` |
| `sandbox/` | The `Runner` interface; engines in `goja/`, `starlark/`, `lua/` |
| `agent/` | Search → prompt → model → sandbox, with a repair loop, typed answers and approval plans |
| `examples/todo/` | The smallest end-to-end example, also served over MCP |
| `examples/shop/` | The quickstart: Go-struct capabilities, a typed answer, an approved refund |
| `examples/combined/` | Go functions, a proto service and an MCP server in one program |
| `examples/taskreport/` | A complete app: SQLite tasks, 23 typed capabilities, roles, reporting, approval in the chat |
| `docs/` | The published documentation site |
| `notes/` | Internal writing: PRDs, milestones, decisions. Never published. |

## Commands

| Command | What it does |
| --- | --- |
| `make generate` | Regenerate Go from every proto module and rebuild the example's `registry.json` |
| `make test` | Buf lint plus all Go tests |
| `make test-vector` | SQLite vector search; needs `SQLITE_VECTOR_EXTENSION` |
| `make test-consumer` | Checks a consumer links only what it blank-imports |
| `make test-env` | Checks no library package reads the environment outside `env.go` |
| `make test-postgres` | pgvector search against a throwaway container |
| `make docs` | Serve the documentation site |

Configuration is entirely environment-driven and entirely optional — see
[`.env.example`](.env.example).

> [!WARNING]
> The default engines run in-process and are not a defence against a determined
> attacker. For programs steered by untrusted users, use process isolation (`SANDBOX_ISOLATION=process`),
> and a container for multi-tenant input. See [SECURITY.md](SECURITY.md).
