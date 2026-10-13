# todo

The smallest complete code-mode application: three capabilities (`todos.create`,
`todos.list`, `todos.complete`) generated from one proto file, an in-memory
store, and a model that answers a request by writing **one program** that calls
them.

It runs with no API key, no database and no Docker. Start here; move to
[`taskreport`](../taskreport) for a web UI, roles and a real database.

```
proto/todo.proto      the contract: three RPCs and their descriptions
gen/                  generated Go, committed so `go run` needs no buf
app/                  ExecuteFunc: RPC path → in-memory todo list
search/               keyword ranking over the registry
main.go               the command-line demo
cmd/todo-mcp/         the same app as an MCP server for Claude Code, Cursor, ...
```

All commands below run from the **repository root**.

## 1. Run it with no key

```sh
make demo-todo        # or: go run ./examples/todo
```

```
search
  keyword ranker (set CAPABILITY_DB_DSN for semantic search)
request
  add two todos and finish the first one

model
  none (set ANTHROPIC_API_KEY or OPENAI_API_KEY to generate the program)

sandbox
  goja / JavaScript (in-process, 30s timeout, 50 call budget)

search
  todos.create -> todo.v1.TodoService/Create
  ...
prompt
  (the exact system prompt a model would receive)

program (fixed, javascript)
  codemode.execute("todos.create", { title: "Document code mode" });
  ...

answer
  completed Document code mode, 1 left
```

Without a key, a fixed program stands in for the model. Everything else is real:
the registry is read from the generated descriptors, search ranks the
capabilities, the prompt is built, and the program runs in the sandbox against
the app.

## 2. Add a model

Set one key. Claude is preferred when both are set.

```sh
export ANTHROPIC_API_KEY=sk-ant-...
go run ./examples/todo -request "add three todos about the launch and finish the last one"
```

Or any OpenAI-compatible endpoint:

```sh
export OPENAI_API_KEY=sk-...
export OPENAI_BASE_URL=https://api.deepseek.com/v1   # omit for OpenAI itself
export OPENAI_MODEL=deepseek-chat
go run ./examples/todo -request "what is left to do?"
```

The output now shows the model, the program it wrote, how long each step took,
and the answer.

| Flag | Default | |
| --- | --- | --- |
| `-request` | `add two todos and finish the first one` | what to ask |
| `-model` | `$ANTHROPIC_MODEL` / `$OPENAI_MODEL` | model name |
| `-temperature` | unset | negative leaves the provider default |
| `-instructions` | — | extra guidance appended to the system prompt |

Every model setting is in [Configuration](../../docs/content/configuration.md#models).

## 3. Try another language

The sandbox language is configuration, not code. The model is prompted in
whichever language is chosen, and the fixed program has a version for each.

```sh
SANDBOX_ENGINE=starlark go run ./examples/todo    # a small Python dialect
SANDBOX_ENGINE=lua      go run ./examples/todo
```

## 4. Run programs out of process

Development runs programs inside the demo's own process. Production should run
them in `codemode-worker`, a separate process with a heap cap and no access to
your environment. Try it locally:

```sh
make worker                                        # builds bin/codemode-worker
SANDBOX_ISOLATION=process SANDBOX_WORKER=$PWD/bin/codemode-worker go run ./examples/todo
```

```
sandbox
  goja / javascript (child process, 30s timeout, 50 call budget, 256 MiB heap)
```

Same program, same answer; it ran in a child process. If the worker is missing
or broken, the demo stops at startup with `codemode-worker check failed`
instead of failing on the first request. Everything about the worker is in
[Running the worker](../../docs/content/worker.md).

## 5. Use it from Claude Code or Cursor

`cmd/todo-mcp` serves the same app over MCP, with two tools:
`search_capabilities` and `run_program`. The client's own model writes the
program.

```sh
make worker
claude mcp add todo -- make -s -C "$PWD" mcp-todo
```

Then ask Claude Code something like *"use the todo tools to add two todos and
complete one"*. `make mcp-todo` runs programs with `SANDBOX_ISOLATION=process`,
because an MCP client is a program author you do not control. See
[Serving MCP clients](../../docs/content/mcp.md).

For Cursor or Claude Desktop, the command is the same:
`make -s -C /path/to/codemode mcp-todo`. The `-s` matters: MCP uses stdout, and
make must not print to it.

## 6. Semantic search (optional)

Keyword ranking is right for three capabilities. To try the vector index:

```sh
export CAPABILITY_DB=sqlite
export CAPABILITY_DB_DSN=capabilities.db
export SQLITE_VECTOR_EXTENSION=/absolute/path/to/vector.dylib   # vector.so on Linux
export OPENAI_API_KEY=sk-...                                     # for embeddings
go run ./examples/todo
```

Or Postgres with pgvector: `CAPABILITY_DB=postgres` and a `postgres://` DSN. The
index is rebuilt on every start because the registry is tiny. See
[Search](../../docs/content/search.md).

## Changing the contract

Edit `proto/todo.proto`, then from the repository root:

```sh
make generate
```

`main.go` reads the registry from the generated descriptors at startup, so there
is nothing else to update. Add the new RPC's case to `app/app.go`; until you do, a
program calling it gets `RPC "..." is not implemented`.

## Tests

```sh
go test ./examples/todo/...
```

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `model: none` | set `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` |
| Every model call 404s | the OpenAI-compatible base URL is missing `/v1` |
| `codemode-worker check failed` | run `make worker` and pass `SANDBOX_WORKER=$PWD/bin/codemode-worker` |
| `a worker is configured but isolation is inprocess` | add `SANDBOX_ISOLATION=process`, or unset `SANDBOX_WORKER` |
| `capability store is configured but embeddings are not` | set `OPENAI_API_KEY` (or `EMBEDDING_API_KEY`), or unset `CAPABILITY_DB_DSN` |
| The MCP client shows a parse error on start | the command is missing `make -s` |
| Todos disappear between runs | expected: the store is in memory |
