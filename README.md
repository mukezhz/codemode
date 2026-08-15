# Code Mode

Turn protobuf RPCs into capabilities a model can compose inside a sandbox.

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
see [`examples/taskreport`](examples/taskreport):

```sh
cd examples/taskreport/web && pnpm install && pnpm build && cd -
go run ./examples/taskreport -serve :8080 \
  -base-url https://api.deepseek.com/v1 -api-key sk-... -model deepseek-chat
```

Ask it *"what is overdue, and which of it is high priority?"* and one model turn
produces one program, which returns a document the browser renders as stat
cards, a chart, and a table. Works against any OpenAI-compatible endpoint.

## Documentation

Full docs are a VitePress site under [`docs/`](docs/content):

- [What code mode is](docs/content/overview.md)
- [**Add it to your application**](docs/content/using-it.md) — empty `main.go` to a working request
- [What your binary links](docs/content/dependencies.md) — the blank imports, and why
- [Permissions](docs/content/permissions.md) — roles in the contract, rows in your code
- [Many services, many users](docs/content/scaling-up.md) — composing proto files, and per-user scoping
- [The flow, step by step](docs/content/flow.md) — one request end to end
- [Capabilities](docs/content/capabilities.md) · [Search](docs/content/search.md) · [Models](docs/content/models.md) · [Sandbox](docs/content/sandbox.md)
- [Configuration](docs/content/configuration.md) · [Troubleshooting](docs/content/troubleshooting.md)

```sh
make docs        # serve locally with hot reload
make docs-build  # build the static site
```

## Layout

| Path | What it is |
| --- | --- |
| `proto/` | `codemode.proto`: the two annotations, embedded for consumers |
| `registry/` | Builds a registry from linked descriptors or a Buf descriptor set; CLI in `cmd/codemode-registry/` |
| `gen/` | Generated Go for `codemode.proto`. Each example generates into its own tree |
| `capability/` | Registry types and the vector index; backends in `sqlitestore/` and `pgstore/` |
| `codemode/` | The two-method public interface: `Search` and `Execute` |
| `llm/` | The `Client` interface; providers in `claude/` and `openai/` |
| `sandbox/` | The `Runner` interface; engines in `goja/`, `starlark/`, `lua/` |
| `agent/` | Search → prompt → model → sandbox, with a repair loop |
| `examples/todo/` | The smallest end-to-end example |
| `examples/taskreport/` | A complete app: SQLite tasks, 18 capabilities, reporting |
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
> All three sandbox engines run in-process. They deny ambient capabilities and
> bound time and call count, which is right for code your own host asked a model
> to write. They are not a defence against a determined attacker. Before running
> programs authored for untrusted end users, implement `Runner` over a separate
> process or a container — see [the isolation ladder](docs/content/sandbox.md#the-isolation-ladder).
