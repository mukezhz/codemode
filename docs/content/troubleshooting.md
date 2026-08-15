# Troubleshooting

## Registry generation

| Error | Cause and fix |
| --- | --- |
| `custom option ... is not defined` | Import `code_mode.proto` so its definitions enter the descriptor set. |
| `service option (code_mode.namespace) must match ...` | Set an explicit lower-camel-case namespace on the service. |
| `method option (code_mode.description) is required` | Add a meaningful description option to the RPC. |
| `streaming RPCs are not supported` | Model the capability as a unary RPC. |
| `duplicate capability` | Give services distinct namespaces, or rename the RPC. |
| `message ... not found` | The request or response type is missing from the descriptor set. Check the import. |

## Search and the index

| Error | Cause and fix |
| --- | --- |
| `sqlite-vector extension path is required` | Set `SQLITE_VECTOR_EXTENSION` to an absolute path. The package never searches for one. |
| `load sqlite-vector extension` | Check platform, sqlite-vector version, file permissions, and architecture. |
| `enable pgvector` | Install pgvector, or grant the role `CREATE` on the database. |
| `capability index uses embedding model ...` | Open with the original model, or rebuild with the new one. |
| `query embedding has dimension ...` | The embedder changed since the rebuild. Rebuild the index. |
| `read capability index metadata` | The index has never been built. Run `capability.Rebuild` first. |
| `no capability store configured` | Set `CAPABILITY_DB_DSN`. Without it, search falls back to keywords. |

## Models

| Error | Cause and fix |
| --- | --- |
| `no LLM provider configured` | Set `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, or `LLM_BASE_URL`. |
| `LLM_PROVIDER "x" is not supported` | Use `claude` or `openai`. Other vendors go through `openai` plus a base URL. |
| `claude: request was declined` | A safety classifier declined. The server-side fallback handles most cases; it is not sent against a custom base URL. |
| `openai: empty response` | Check the finish reason in the error. Usually the model hit a token cap. |
| `capability store is configured but embeddings are not` | Set `EMBEDDING_API_KEY` or `EMBEDDING_BASE_URL`. |

## Sandbox

| Error | Cause and fix |
| --- | --- |
| `SANDBOX_ENGINE "x" is not supported` | Use `goja`, `starlark`, or `lua`. |
| `sandbox stopped: context deadline exceeded` | The program ran past `SANDBOX_TIMEOUT`. Raise it, or look for an unbounded loop. |
| `call budget of N exhausted` | The program made more capability calls than `SANDBOX_MAX_CALLS`. Raise it, or check whether the program is calling in a loop it should have batched. |
| `capability "x" is not registered` | The model invented a name. Expected, and correctly rejected — the repair loop feeds it back. Persistent cases usually mean a description is unclear. |
| `sandbox: ... has no .execute field` | The engine's API object was built wrong. All three bind `codemode.execute`; file a bug. |

## Common confusions

**A capability ran that search did not return.** Working as designed. Search
decides what the model *knows about*; the registry decides what may *run*. They
are separate lists — see [the flow](/flow#_4-the-model-writes-a-program).

**The model wrote the wrong language.** The sandbox engine writes the prompt, so
this means `SANDBOX_ENGINE` is not what you think. Check `Runner.Name()`, which
the demo prints.

**The demo answered without a model.** With no `ANTHROPIC_API_KEY` or
`OPENAI_API_KEY`, a fixed program stands in. The line under `model` says so.

**Prompt caching never hits.** Declarations are sorted for exactly this reason,
so the usual cause is a changing match set — a different `limit`, or a search
backend that reorders ties.

## Getting more detail

The Todo demo prints every swappable piece as it is selected, then the prompt,
the program, and the answer:

```sh
go run ./examples/todo -request "your request here"
```

That output is the fastest way to see which engine, provider, and search backend
are actually in play.
