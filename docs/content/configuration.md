# Configuration

Everything is environment-driven, and nothing is required — with no configuration
at all the Todo demo still runs end to end, using keyword search and a fixed
program.

A copy of this lives in `.env.example` at the repository root.

## Models

| Variable | Values | Default |
| --- | --- | --- |
| `LLM_PROVIDER` | `claude`, `openai` | whichever key is set, Claude first |
| `LLM_BASE_URL` | any compatible endpoint | the vendor's |
| `ANTHROPIC_API_KEY` | | — |
| `ANTHROPIC_MODEL` | | `claude-opus-5` |
| `ANTHROPIC_BASE_URL` | wins over `LLM_BASE_URL` | — |
| `OPENAI_API_KEY` | | — |
| `OPENAI_MODEL` | | `gpt-5` |
| `OPENAI_BASE_URL` | wins over `LLM_BASE_URL` | — |

### Generation settings

Shared by every provider. All optional.

| Variable | Values | Default |
| --- | --- | --- |
| `LLM_MAX_TOKENS` | integer | `16000`, sized for thinking plus a program |
| `LLM_TIMEOUT` | Go duration, e.g. `45s` | the SDK's own |
| `LLM_TEMPERATURE` | float | unset — the provider's own default |
| `LLM_TOP_P` | float | unset |

Anything set in code wins over these, so an application can pin a value a
deployment cannot override by accident. A base URL may be set without an API
key, for endpoints that do not authenticate.

::: warning
`LLM_TEMPERATURE` and `LLM_TOP_P` turn Claude's extended thinking off —
Anthropic rejects sampling parameters alongside it. Leave both unset unless you
have a reason. See [Models](/models#configuring-the-model).
:::

## Search

| Variable | Values | Default |
| --- | --- | --- |
| `CAPABILITY_DB` | `sqlite`, `postgres` | `sqlite` |
| `CAPABILITY_DB_DSN` | file path, or connection URL | unset — falls back to keyword search |
| `SQLITE_VECTOR_EXTENSION` | absolute path to `vector.dylib` / `vector.so` | — |

## Embeddings

| Variable | Values | Default |
| --- | --- | --- |
| `EMBEDDING_MODEL` | any embeddings model | `text-embedding-3-small` |
| `EMBEDDING_API_KEY` | | falls back to `OPENAI_API_KEY` |
| `EMBEDDING_BASE_URL` | | falls back to `OPENAI_BASE_URL`, then `LLM_BASE_URL` |

Always the OpenAI embeddings protocol, regardless of `LLM_PROVIDER` — Anthropic
publishes no embeddings API. See [Search](/search#embeddings).

## Sandbox

| Variable | Values | Default |
| --- | --- | --- |
| `SANDBOX_ENGINE` | the language: `goja`, `starlark`, `lua` | `goja` |
| `SANDBOX_ISOLATION` | `inprocess`, `process` | `inprocess` |
| `SANDBOX_TIMEOUT` | Go duration, e.g. `10s` | `30s` |
| `SANDBOX_MAX_CALLS` | integer | `50` |
| `SANDBOX_WORKER` | argv for `codemode-worker`, space-separated; `process` only | `codemode-worker` on `PATH` |
| `SANDBOX_WORKER_CONTAINER` | `true` when `SANDBOX_WORKER` starts a container | `false` |
| `SANDBOX_MAX_HEAP_BYTES` | integer; `process` isolation only | `268435456` |

Options passed in code win over these. See [Sandbox](/sandbox) and
[Running the worker](/worker).

`sandbox.New` refuses a configuration that is probably a mistake rather than
guessing:

| Configuration | Error |
| --- | --- |
| an isolation that is not `inprocess` or registered | `sandbox isolation "x" is unknown` |
| `process` without importing `sandbox/subprocess` | `... is not registered: add _ ".../sandbox/subprocess"` |
| `SANDBOX_WORKER` or `SANDBOX_WORKER_CONTAINER` with `inprocess` | `a worker is configured but isolation is inprocess` |
| `SANDBOX_WORKER_CONTAINER=true` with no `SANDBOX_WORKER` | `worker container is set but no worker command is` |
| `process`, but the worker does not start or answer | `codemode-worker check failed (...)` |

The last check runs one trivial program through the worker inside `New`, so a
missing binary, a version mismatch, a bad `docker run` or a missing image fails at
startup, not on the first request.

## Observing calls

There is no tracing dependency. `agent.Options.OnCall` sees every capability
call as it finishes; feed it to whatever the host already runs. It must not block.

```go
agent.Run(ctx, mode, model, request, agent.Options{
    Runner: runner,
    OnCall: func(call codemode.Call) {
        slog.Info("capability", "user", user, "name", call.Name,
            "ms", call.Duration.Milliseconds(), "error", call.Error)
    },
})
```

The same hook is where an audit log or an OpenTelemetry span per call goes.

## Commands

| Command | What it does |
| --- | --- |
| `mise install` | Install pinned tools: Buf, Node, pnpm |
| `make generate` | Regenerate Go from every proto module and the example's `registry.json` |
| `make demo-todo` | Regenerate, then run the Todo example end to end |
| `make test` | Buf lint plus all Go tests |
| `make test-vector` | SQLite vector search; needs `SQLITE_VECTOR_EXTENSION` |
| `make test-postgres` | pgvector search against a throwaway container |
| `make docs` | Serve these docs locally with hot reload |
| `make docs-build` | Build the static docs site |

Every command works from the repository root and exits non-zero on failure.

## Combinations worth knowing

**Fully local, nothing leaves the machine.** Ollama for both the model and the
embeddings, SQLite for the index:

```sh
LLM_BASE_URL=http://localhost:11434/v1
OPENAI_MODEL=qwen2.5-coder
EMBEDDING_MODEL=nomic-embed-text
CAPABILITY_DB=sqlite
CAPABILITY_DB_DSN=capabilities.db
SQLITE_VECTOR_EXTENSION=/usr/local/lib/vector.so
```

**No CGO.** Postgres avoids the SQLite C dependency entirely:

```sh
CAPABILITY_DB=postgres
CAPABILITY_DB_DSN=postgres://user:pass@localhost:5432/codemode?sslmode=disable
```

**Tighter blast radius.** Starlark has no `while` loops or recursion, and a small
budget bounds a misbehaving program hard:

```sh
SANDBOX_ENGINE=starlark
SANDBOX_TIMEOUT=5s
SANDBOX_MAX_CALLS=10
```
