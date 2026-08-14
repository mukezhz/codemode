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

The repository discovers an RPC; it does not invoke it. A consumer uses the
returned `Capability.RPC` and input schema to call its own RPC transport.

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
