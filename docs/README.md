# Developer guide

This repository turns protobuf RPCs into discoverable capabilities. The build
produces a JSON registry from the contracts; application code can then embed
that registry into SQLite and search it by natural-language intent.

## End-to-end flow

```text
proto/*.proto
    |
    | make generate
    v
registry/protoset.pb  -- registry generator -->  registry/registry.json
                                                       |
                                                       | capability.Rebuild
                                                       v
                                                SQLite vector index
                                                       |
                                                       | capability.Open + Search
                                                       v
                                                ranked capabilities
                                                       |
                                                       v
                                                invoke capability.rpc
```

The core packages discover an RPC; they do not invoke it. A consumer uses the
returned `Capability.RPC` and input schema to call its own RPC transport. The
Todo example below supplies a small in-process transport to make that boundary
runnable.

## How the complete code-mode workflow fits together

The intended request lifecycle is:

1. A developer defines trusted application operations as protobuf RPCs with an
   explicit namespace and description.
2. `make generate` turns those contracts into a registry. The registry is the
   allowlist and schema catalog presented to code mode.
3. `capability.Rebuild` embeds the registry. This happens when contracts or the
   embedding model change, not once per user request.
4. For a user request, `Index.Search` selects a small set of relevant
   capabilities. For example, “finish the first task” should retrieve
   `todos.list` and `todos.complete`.
5. A model receives only those capability names, descriptions, and input/output
   schemas, then writes a short program using them.
6. An isolated sandbox runs that untrusted program. It can compute and compose
   calls, but it cannot directly access the application database or secrets.
7. A host-controlled bridge receives a call such as
   `call("todos.complete", {"id": 1})`, checks that the capability was allowed
   for this run, validates the input, resolves the registry entry to
   `todo.v1.TodoService/Complete`, and invokes the trusted RPC transport.
8. The bridge returns a JSON result to the program. The program can use it in
   later calls, and its final value becomes the answer to the user.

```text
user request
    |
    v
capability search --> selected allowlist + schemas --> model-generated code
                                                        |
                                                        v
                                                isolated sandbox
                                                        |
                                             call(name, JSON input)
                                                        |
                                                        v
                                       host validation + RPC transport
                                                        |
                                                        v
                                               trusted application
```

Search and execution are separate on purpose. Search decides what the model may
know about; the host allowlist decides what generated code may execute.

### What the sandbox must enforce

Generated code is untrusted. A production sandbox must provide real process or
container isolation and enforce:

- A per-run capability allowlist. A registered capability is not automatically
  allowed for every user or request.
- No application credentials inside the sandbox. Credentials stay in the host
  RPC layer.
- Denied network and host filesystem access unless a specific workflow needs
  them.
- CPU, memory, wall-clock, output-size, and capability-call limits.
- A fresh session or an explicitly scoped session identifier, followed by
  cleanup.
- Host-side input validation, authentication, authorization, rate limits, and
  audit logs for every capability call.

Do not implement this with `os/exec` alone or call an in-process interpreter a
sandbox. A container sandbox such as Cloudflare Sandbox SDK can own code
execution, while a separate host bridge owns capability authorization and RPC
calls. The precise bridge transport is not implemented in this repository yet.

### What exists today

| Layer | Status |
| --- | --- |
| Explicit protobuf capability metadata | Implemented |
| Registry generation and schema validation | Implemented |
| SQLite vector indexing and semantic search | Implemented |
| Todo capability invocation example | Implemented in process for demonstration |
| Model prompt/code generation | Not implemented |
| Generated script SDK such as `codemode.todos.create()` | Not implemented |
| Real gRPC transport | Not implemented |
| Secure sandbox and host bridge | Not implemented |

## Runnable Todo application

`proto/todo.proto` defines three capabilities:

| Capability | RPC | Effect |
| --- | --- | --- |
| `todos.create` | `todo.v1.TodoService/Create` | Creates an item |
| `todos.list` | `todo.v1.TodoService/List` | Lists current items |
| `todos.complete` | `todo.v1.TodoService/Complete` | Completes an item by ID |

Run the complete contract-to-invocation example from the repository root:

```sh
make demo-todo
```

The target regenerates the registry, loads it, resolves capability names to RPC
paths, validates JSON inputs, and executes this sequence against an in-memory
Todo application:

```text
todos.create
todos.create
todos.list
todos.complete
todos.list
```

The final list shows item `1` completed. This demonstrates the same boundary a
future sandbox bridge will use, but it is intentionally in-process and is not a
security sandbox. It also starts after capability selection, so sqlite-vector
is not required to run it; semantic selection remains covered by
`make test-vector`.

## 1. Define a capability

Capabilities start as unary protobuf RPCs in `proto/`:

```proto
import "code_mode.proto";

service StudentService {
  option (code_mode.namespace) = "students";

  rpc InterviewsWithin(InterviewsWithinRequest)
      returns (InterviewsWithinResponse) {
    option (code_mode.description) =
        "Returns students with an interview in the requested period.";
  }
}
```

The generator requires:

- An import of `code_mode.proto`.
- An explicit, lower-camel-case `(code_mode.namespace)` service option.
- A non-empty `(code_mode.description)` option on every RPC. This becomes its
  searchable description.
- Unique generated names. The example becomes
  `students.interviewsWithin`.
- Unary RPCs. Client- and server-streaming methods are rejected.
- Request and response messages present in the descriptor set.

Field names remain protobuf `snake_case`. Registry field types use a compact
script-oriented form:

| Protobuf | Registry type |
| --- | --- |
| `bool` | `boolean` |
| `string` | `string` |
| `bytes` | `Uint8Array` |
| Numeric scalar | `number` |
| Message or enum | Its unqualified type name |
| `repeated T` | `T[]` |
| `map<K, V>` | `Record<K, V>` |

Schemas describe the direct fields of each RPC request and response. Referenced
message types are named, not recursively expanded.

## 2. Generate the registry

Install the pinned Buf version once, then generate from the repository root:

```sh
mise install
make generate
```

`make generate` runs two steps:

1. Buf compiles `proto/` and its custom options into `registry/protoset.pb`.
2. `go run ./registry` validates every service and writes
   `registry/registry.json`.

Both outputs are generated and ignored by Git. Regenerate after every protobuf
contract or capability option change. The registry is deterministic for a given
descriptor ordering and has this shape:

```json
{
  "capabilities": [{
    "name": "students.interviewsWithin",
    "rpc": "student.v1.StudentService/InterviewsWithin",
    "description": "Returns students with an interview in the requested period.",
    "input": {
      "type": "InterviewsWithinRequest",
      "fields": { "days": "number" }
    },
    "output": {
      "type": "InterviewsWithinResponse",
      "fields": { "students": "Student[]" }
    }
  }]
}
```

Shared JSON types live in `capability/schema.go`; the generator aliases them so
generation and search cannot drift onto separate schemas.

## 3. Build the semantic index

The `capability` package deliberately does not choose an embedding provider.
Supply a stable model name and a function that returns `[]float32`:

```go
registryBytes, err := os.ReadFile("registry/registry.json")
if err != nil {
    return err
}

var registry capability.Registry
if err := json.Unmarshal(registryBytes, &registry); err != nil {
    return err
}

embedder := capability.Embedder{
    Model: "provider/model-version",
    Embed: embedText,
}

if err := capability.Rebuild(
    ctx,
    "capabilities.db",
    os.Getenv("SQLITE_VECTOR_EXTENSION"),
    registry,
    embedder,
); err != nil {
    return err
}
```

`Rebuild` embeds each capability's name, description, RPC path, input schema,
and output schema. Every vector must be non-empty and have the same dimension.
It then replaces the `capabilities` and `capability_index_meta` tables in one
transaction, initializes cosine distance through sqlite-vector, and records the
embedding dimension and model name.

Rebuilding is a replacement operation for those two tables. Use a dedicated
database file, and rebuild whenever the generated registry or embedding model
changes.

Runtime requirements:

- `github.com/mattn/go-sqlite3`, so builds require CGO and a C compiler.
- sqlite-vector 1.0.0 as a native library (`vector.dylib` on macOS or
  `vector.so` on Linux).
- An explicit absolute extension path. The package never searches for or loads
  an arbitrary extension.

## 4. Open and search the index

Use the same embedding model at query time:

```go
index, err := capability.Open(
    ctx,
    "capabilities.db",
    os.Getenv("SQLITE_VECTOR_EXTENSION"),
    embedder,
)
if err != nil {
    return err
}
defer index.Close()

matches, err := index.Search(ctx, "find unpaid bills", 5)
if err != nil {
    return err
}
for _, match := range matches {
    fmt.Printf("%s %s %.4f\n",
        match.Capability.Name,
        match.Capability.RPC,
        match.Distance,
    )
}
```

`Search` rejects blank queries and non-positive limits. It embeds the query,
checks its dimension against the stored metadata, and returns nearest matches
ordered by ascending cosine distance. Lower distance means a closer match.
`Open` also rejects an embedder whose model name differs from the one used to
build the index.

The index keeps one SQLite connection open because native extension loading is
connection-specific. Call `Close` when the consumer shuts down.

## Development workflow

After changing protobufs or generator behavior:

```sh
make generate
make test
```

`make test` runs Buf lint and all Go tests. The native vector integration test
skips when no extension is configured. Run it explicitly with:

```sh
SQLITE_VECTOR_EXTENSION=/absolute/path/vector.dylib make test-vector
```

Use `/absolute/path/vector.so` on Linux.

Tests are split by responsibility:

- `registry/main_test.go` checks descriptor-to-registry generation.
- `capability/index_test.go` checks searchable embedding content and, when the
  native extension is available, index rebuild/open/search behavior.

## Common failures

| Error | Cause and fix |
| --- | --- |
| `custom option ... is not defined` | Import `code_mode.proto` so its definitions enter the descriptor set. |
| `service option (code_mode.namespace) must match ...` | Set an explicit lower-camel-case namespace on the service. |
| `method option (code_mode.description) is required` | Add a meaningful description option to the RPC. |
| `streaming RPCs are not supported` | Model the capability as a unary RPC. |
| `duplicate capability` | Give services distinct namespaces or rename the RPC. |
| `sqlite-vector extension path is required` | Pass an explicit native library path. |
| `load sqlite-vector extension` | Check the platform, sqlite-vector version, file permissions, and architecture. |
| `capability index uses embedding model ...` | Open with the original model or rebuild with the new one. |
| `query embedding has dimension ...` | Use the same model/configuration used during rebuild. |

## Ownership map

| Path | Responsibility |
| --- | --- |
| `proto/code_mode.proto` | Shared namespace and description option definitions |
| `proto/*.proto` | Source RPC contracts and explicit discovery metadata |
| `registry/main.go` | Descriptor validation and JSON generation |
| `capability/schema.go` | Shared registry data model |
| `capability/index.go` | Embedding, SQLite index lifecycle, and search |
| `Makefile` | Supported generation and verification entry points |
| `registry/protoset.pb` | Ignored generated descriptor set |
| `registry/registry.json` | Ignored generated capability registry |
