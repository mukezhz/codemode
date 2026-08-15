# Search

Search decides which capabilities the model is told about. It runs on the host,
before the model is involved, and it has no bearing on what may actually run —
see [the two lists](/flow#_4-the-model-writes-a-program).

## The interface

Any function with this shape works:

```go
type SearchFunc func(ctx context.Context, query string, limit int) ([]capability.Match, error)
```

Matches are ordered closest first; a lower `Match.Distance` is a stronger match.
The demo ships a dependency-free keyword ranker so it runs without a database.
For anything real, use the semantic index.

## The store interface

The index keeps embedded contracts and answers nearest-neighbour queries. Which
database does that is behind one interface:

```go
type Store interface {
    Rebuild(ctx context.Context, model string, dimension int, records []Record) error
    Metadata(ctx context.Context) (model string, dimension int, err error)
    Search(ctx context.Context, vector []float32, limit int) ([]Match, error)
    Close() error
}
```

Both backends rank by cosine distance, so results are comparable across them.

```mermaid
flowchart TD
  index["capability.Index<br/><small>embeds, checks dimensions, ranks</small>"]
  index --> store["capability.Store"]
  store --> sqlite["sqlite + sqlite-vector<br/><small>vector_full_scan</small>"]
  store --> pg["postgres + pgvector<br/><small>the &lt;=&gt; operator</small>"]
```

## Choosing a backend

```sh
CAPABILITY_DB=sqlite                       # or postgres
CAPABILITY_DB_DSN=capabilities.db          # or postgres://user:pass@host/db
SQLITE_VECTOR_EXTENSION=/abs/path/vector.dylib
```

`capability.OpenStore(ctx)` reads those and hands back a `Store`. Leave
`CAPABILITY_DB_DSN` unset and the demo falls back to keyword ranking.

### SQLite

Needs [sqlite-vector](https://github.com/asg017/sqlite-vector) 1.0.0 as a native
library — `vector.dylib` on macOS, `vector.so` on Linux — and an **explicit
absolute path**. The package never searches for or loads an arbitrary extension.

Because `github.com/mattn/go-sqlite3` is used, builds require CGO and a C
compiler. One connection is held open, since extension loading is
connection-specific.

### Postgres

Needs [pgvector](https://github.com/pgvector/pgvector). `OpenPostgres` runs
`CREATE EXTENSION IF NOT EXISTS vector` on connect, so the role needs `CREATE` on
the database, or the extension installed already. No CGO.

Search is a full scan (`ORDER BY distance LIMIT n`). That is correct at any size
and fast enough for a registry; add an HNSW index if you ever index something
much larger.

Exercise it against a throwaway database:

```sh
make test-postgres
```

## Embeddings

The `capability` package does not choose an embedding provider. Supply a stable
model name and a function returning `[]float32`:

```go
embedder := capability.Embedder{
    Model: "provider/model-version",
    Embed: embedText,
}
```

The model name is recorded in the index. `Open` rejects an embedder whose name
differs from the one used to build it, so a silent mismatch cannot return
nonsense.

`llm.NewEmbedder()` provides one over the OpenAI embeddings protocol. Anthropic
publishes no embeddings API, so this path is used regardless of `LLM_PROVIDER`;
point `EMBEDDING_BASE_URL` at a local endpoint to embed without leaving the
machine.

```sh
EMBEDDING_MODEL=text-embedding-3-small
EMBEDDING_API_KEY=...          # falls back to OPENAI_API_KEY
EMBEDDING_BASE_URL=...         # falls back to OPENAI_BASE_URL, then LLM_BASE_URL
```

## Build and query

```go
store, err := capability.OpenStore(ctx)
if err != nil {
    return err
}
defer store.Close()

if err := capability.Rebuild(ctx, store, registry, embedder); err != nil {
    return err
}

index, err := capability.Open(ctx, store, embedder)
if err != nil {
    return err
}
defer index.Close()

matches, err := index.Search(ctx, "find unpaid bills", 5)
```

`Rebuild` embeds each capability's name, description, RPC path, and input and
output schemas. Every vector must be non-empty and the same dimension. It then
replaces the index contents in one transaction and records the dimension and
model name.

::: warning Rebuild replaces, it does not merge
Use a dedicated database, and rebuild whenever the generated registry or the
embedding model changes. It is not a per-request operation — the demo rebuilds on
every run only because its registry is tiny.
:::

`Search` rejects blank queries and non-positive limits, embeds the query, checks
its dimension against the stored metadata, and returns nearest matches by
ascending cosine distance.

`index.Search` matches `codemode.SearchFunc`, so it can be passed straight to
`codemode.New`.
