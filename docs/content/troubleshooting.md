# Troubleshooting

## Registry generation

| Error | Cause and fix |
| --- | --- |
| `custom option ... is not defined` | Import `codemode.proto` so its definitions enter the descriptor set. |
| `service option (codemode.namespace) must match ...` | Set an explicit lower-camel-case namespace on the service. |
| `method option (codemode.description) is required` | Add a meaningful description option to the RPC. |
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
| `LLM provider "x" is not registered: add _ "…/llm/x"` | Blank-import the provider package. |
| `LLM provider "x" is unknown` | Use `claude` or `openai`. Other vendors go through `openai` plus a base URL. |
| `claude: request was declined` | A safety classifier declined. The server-side fallback handles most cases; it is not sent against a custom base URL. |
| `openai: empty response` | Check the finish reason in the error. Usually the model hit a token cap. |
| `capability store is configured but embeddings are not` | Set `EMBEDDING_API_KEY` or `EMBEDDING_BASE_URL`. |

## Sandbox

| Error | Cause and fix |
| --- | --- |
| `sandbox engine "x" is not registered: add _ "…/sandbox/x"` | Blank-import the engine package. |
| `sandbox engine "x" is unknown` | `SANDBOX_ENGINE` is the language: `goja`, `starlark`, or `lua`. |
| `sandbox isolation "x" is unknown` / `is not registered` | `SANDBOX_ISOLATION` is `inprocess` or `process`; `process` needs `_ "…/sandbox/subprocess"`. |
| `a worker is configured but isolation is inprocess` | Set `SANDBOX_ISOLATION=process`, or remove `SANDBOX_WORKER` and `SANDBOX_WORKER_CONTAINER`. |
| `codemode-worker check failed (...)` | The worker did not start or answer at startup. See [Running the worker](/worker#troubleshooting). |
| `sandbox: memory limit of N bytes exceeded` | With `process` isolation the program passed `SANDBOX_MAX_HEAP_BYTES`. Usually a runaway program. |
| `sandbox stopped: context deadline exceeded` | The program ran past `SANDBOX_TIMEOUT`. Raise it, or look for an unbounded loop. |
| `call budget of N exhausted` | The program made more capability calls than `SANDBOX_MAX_CALLS`. Raise it, or check whether the program is calling in a loop it should have batched. |
| `capability "x" is not registered` | The model invented a name. Expected, and correctly rejected — the repair loop feeds it back. Persistent cases usually mean a description is unclear. |

## Caching

| Symptom | Cause and fix |
| --- | --- |
| Every request pays for the model; the cache stays empty | Almost always a wrapper that does not forward `Fingerprint`. Without a capability fingerprint the agent turns caching off deliberately. See [Caching programs](/caching#the-capability-fingerprint). |
| A cache hit never happens for reworded questions | `program.NewExactCache()` matches on text only. Use `program.New` with an embedder to hit across rewordings. |
| A reworded question returns the wrong answer | The semantic threshold is too generous. Lower `program.Options.Threshold`; the default of 0.05 is strict on purpose. |
| A user sees an answer scoped to someone else's access | `agent.Options.CacheCaller` was not set. It must carry the role, the tenant, or both. |
| Cached programs vanish after a deploy | Expected when the capability set changed: the fingerprint changed with it, so programs written against the old set are unreachable. |
| `program cache needs a store opened by capability/sqlitestore` | `OpenProgramCache` takes the store that package opened, not one from another backend. |

## Streaming

| Symptom | Cause and fix |
| --- | --- |
| `OnChunk` never fires | The model does not implement `llm.Streamer`, or a wrapper around it does not forward `CompleteStream`. |
| Chunks arrive all at once at the end | Something between you and the browser is buffering. Set `X-Accel-Buffering: no` and flush after each event. |
| The program display doubles up on a retry | A repair attempt streams too. Reset the buffer when `Result.Attempts` changes. |
| Long silence, then the whole answer quickly | Provider time-to-first-token, not your code. See [Latency](/latency#time-to-first-chunk). |

## Common confusions

**Answers are slow.** Log `result.Timing`. It is almost certainly `Generate` —
the model call is typically 99.9% of a request, and the sandbox is under a
millisecond. [Latency](/latency) has the numbers and what to do about them.

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

## Permissions

| Message or symptom | Cause |
| --- | --- |
| `capability "x" is not registered` | Either the caller's role withholds it, or the name does not exist. The two are indistinguishable on purpose. |
| A caller sees fewer capabilities than expected | `(codemode.roles)` on that RPC, or a `Require` call in the host. `Registry.Unrestricted()` lists what nobody restricted. |
| A capability is available to everyone by mistake | Its `(codemode.roles)` was forgotten. Pin `Unrestricted().Names()` in a test — nothing else catches it. |
| `roles option is blank` | The roles string is only whitespace. Remove the line to place no restriction (`""` counts as not set). |
| An unknown role gets some capabilities | Correct: unknown roles get the unrestricted set and nothing more. Roles fail closed, never open. |
| A role edited in the admin screen has no effect | The registry is built from the `capability.Caller` you pass to `For`. Check you fetch roles or permissions per request, and that any cache of that lookup expires or is cleared when a role is saved. |
| A user with several roles gets only one role's capabilities | Pass them all: `For(capability.Caller{Roles: session.Roles})`, not `ForRole(session.Roles[0])`. |
| `permission "x" must look like resource.action` | A proto `(codemode.permissions)` value, or a name passed to `ValidatePermissions`, breaks the pattern: lowercase, dot-separated, at least two parts. |
| `permission "x" is not declared by any capability` | `ValidatePermissions` was given a name no proto declares: a typo, or an RPC that was renamed or removed. |
| A dynamic role gets nothing | Its permissions do not match the names in `(codemode.permissions)` exactly (case and spelling), or you passed them as `Roles`. Pin each role's capability set in a test. |
| A fixed role cannot reach an RPC annotated with permissions | Correct: roles match `(codemode.roles)` only. Add the role to the RPC, or grant the role's users the permission. |

## Wrong answers

| Symptom | Cause |
| --- | --- |
| The answer says someone "has no tasks" when they have several | The model was not told the shape of its own access, so it filtered its own rows by another name. Add per-identity instructions to the prompt. |
| A program calls one capability dozens of times with varying inputs | It is reconstructing data a capability it lacks would have returned. Forbid it in the instructions and set the call budget near what a real answer needs. |
| A field reads as `undefined` in the answer | The model guessed a field name. Referenced message shapes travel in `Capability.Types` and render under "Shapes referred to above" — check the prompt with `-log debug`. |
| An answer looks plausible but is wrong | Read the program. It is logged as a numbered block at every log level, and returned with each chat response. |
