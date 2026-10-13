# Caching programs

Ask the same question twice and the model writes the same program twice, at full
price — several seconds each time. A program is a pure function of the request
and the capabilities available to answer it, so the second one is avoidable.

```
first ask   search + generate + run   ≈ 5.75s
second ask  cache  + run              ≈ 4ms
```

This page is how to turn that on, how to scope it safely, and how to replace any
part of it with your own.

## The quickest version

Two lines. No database, no embedding model:

```go
cache := program.NewExactCache()
defer cache.Close()

result, err := agent.Run(ctx, mode, model, request, agent.Options{
    Runner: runner,
    Cache:  cache,
})
```

That is a working program cache. A repeat of the same question — ignoring
surrounding whitespace and letter case — skips search and the model entirely.

`result.Cached` reports which path answered. On a hit, `result.Matches` is empty
and `result.Attempts` is zero: nothing was searched and nothing was generated.

```go
log.Info("answered",
    "cached", result.Cached,
    "timing", result.Timing.String())
```

::: tip Start here even if you want the semantic cache
The exact cache cannot serve the wrong program for a similar-looking request,
because it has no notion of similar. Get the wiring right with it, then swap the
implementation — nothing else in your code changes.
:::

## The semantic version

To also hit across rewordings — *"show me my tasks"* reusing the program written
for *"what are my tasks?"* — embed the request and reuse the nearest previous
one:

```go
store, err := sqlitestore.OpenProgramCache(ctx, capabilityStore, dimension)
if err != nil {
    return err
}
cache, err := program.New(store, embedder, program.Options{Threshold: 0.05})
if err != nil {
    return err
}
defer cache.Close()
```

It lives in the database you already have. `OpenProgramCache` takes the
`capability.Store` you opened for the capability index and adds a
`cached_programs` table beside `capabilities` — same file or same connection
pool, no second DSN, no second backend to configure. Postgres is identical:

```go
store, err := pgstore.OpenProgramCache(ctx, capabilityStore, dimension)
```

`dimension` comes from the capability index itself, which keeps the two on the
same embedding model by construction:

```go
_, dimension, err := capabilityStore.Metadata(ctx)
```

Closing the cache does **not** close the capability store. They share a
connection and the capability store owns it.

### Choosing the threshold

`Options.Threshold` is the largest cosine distance that still counts as the same
request. It is the only number here worth thinking about, because it decides how
different two requests may be before one's program answers the other.

`DefaultThreshold` is 0.05, deliberately strict. The asymmetry is the reason: a
missed reuse costs one model call — the one you were going to make anyway. A
wrong reuse runs the wrong program. Those are not comparable outcomes.

Test your embedder against pairs that differ in one important word:

| Stored | Candidate | Must |
| --- | --- | --- |
| `list my todos` | `list todos` | hit |
| `list my todos` | `delete my todos` | **miss** |
| `list my todos` | `complete my todos` | **miss** |
| `list my todos` | `list my notes` | **miss** |

A verb swap moves a request about as far as a dropped word does in most
embedding spaces. If your threshold cannot separate them, lower it and accept
the lower hit rate. `program/program_test.go` has this as a test to copy.

## Scoping: who may reuse what

A program is written for one dialect, against one set of capabilities, on behalf
of one caller. Reusing it outside any of those runs code against a world it was
not written for.

```go
type Key struct {
    Request      string // the request, verbatim
    Language     string // from Runner.Language — a JS program is not a Lua one
    Capabilities string // fingerprint of the capability set
    Caller       string // your role or tenant, from Options.CacheCaller
}
```

`Key.Scope()` hashes the last three into one value, and every store confines its
lookup to it. Nothing is shared across scopes.

The agent fills in `Language` and `Capabilities`. **`Caller` is yours**, and it
is the one that matters for [permissions](/permissions):

```go
agent.Options{
    Runner:      runner,
    Cache:       cache,
    CacheCaller: string(identity.Role),   // or tenant, or both
}
```

Leave it empty only when every caller of this agent sees the same capabilities
and the same data. A program written against an admin's capabilities must never
be served to a member — and with the role in the key, it cannot be.

Multi-tenant deployments should include the tenant:

```go
CacheCaller: identity.Tenant + "/" + string(identity.Role),
```

### The capability fingerprint

A program that calls a capability you have since removed must not be reused. The
runtime reports which capabilities exist through:

```go
type Fingerprinter interface {
    Fingerprint() string
}
```

The runtime `codemode.New` returns implements it, hashing the full contract
of every registered capability: name, RPC path, description, schemas and types,
roles, permissions, and read-only flag. Change any of it and every program cached against the old one becomes
unreachable.

::: warning A wrapper must forward Fingerprint
A value stored in an interface loses the methods that interface does not name.
Wrap a `CodeMode` — for tracing, metrics, anything — and the wrapper does not
implement `Fingerprinter` unless you say so. The agent treats a missing
fingerprint as *"caching is not safe here"* and turns the cache off, silently and
correctly, so every request pays full price while your cache sits empty.

```go
func (wrapper *traced) Fingerprint() string {
    fingerprinter, ok := wrapper.inner.(codemode.Fingerprinter)
    if !ok {
        return ""
    }
    return fingerprinter.Fingerprint()
}
```

An empty fingerprint counts as none, so a wrapper with nothing to forward to
cannot accidentally scope every capability set to the same key.
:::

## Staleness

A hit runs code written for an earlier state of the world. If a capability it
calls has changed shape, the program fails.

That is handled rather than prevented. The agent calls `Forget` on the entry and
falls through to generation, so a stale program costs one wasted sandbox run —
tens of microseconds — and removes itself. The next ask caches the replacement.

The cache almost never fails a request. A lookup that errors, a miss, a stale
hit, and a failed write are all just "no cache today", with one exception: a
cached program that wrote before failing, or failed with `codemode.Permanent`,
stops with `agent.ErrNotRetried` instead of being regenerated, because running a
new program could repeat the write. See [Writes and retries](/writes).

::: tip Cached programs, not cached answers
The cached program still runs, still calls into your application, and still sees
today's data. Yesterday's question asked again returns today's numbers. That is
what makes this safe to leave on, and it is the difference between caching a
program and caching a response.
:::

## Bring your own database

If you want the semantic cache but not SQLite or Postgres, implement `Store` —
three methods and a close:

```go
type Store interface {
    Nearest(ctx context.Context, scope string, vector []float32, limit int) ([]Hit, error)
    Put(ctx context.Context, record Record) error
    Delete(ctx context.Context, id string) error
    Close() error
}
```

`program.New` supplies everything above it: embedding the request, applying the
threshold, computing the scope, and healing stale entries. Your store only holds
vectors and answers nearest-neighbour queries within one scope.

The contract:

- **`Nearest` must filter by `scope` before ranking**, and never return a record
  from another scope. This is what keeps roles, tenants, dialects, and capability
  sets apart — the layer above trusts you for it.
- Rank by cosine distance, closest first, so `Threshold` means the same thing
  everywhere.
- `Put` replaces any record in the same scope with the same request text.
- `Delete` on an unknown id is not an error: two requests can fail on the same
  stale entry at once.
- Records with a mismatched vector dimension should be skipped, not raised —
  they were written by a different embedding model.

A sketch against any vector database:

```go
type qdrantStore struct{ client *qdrant.Client }

func (store *qdrantStore) Nearest(ctx context.Context, scope string, vector []float32, limit int) ([]program.Hit, error) {
    found, err := store.client.Query(ctx, &qdrant.QueryPoints{
        CollectionName: "cached_programs",
        Query:          qdrant.NewQuery(vector...),
        Filter:         qdrant.NewFilter(qdrant.NewMatch("scope", scope)), // required
        Limit:          qdrant.PtrOf(uint64(limit)),
        WithPayload:    qdrant.NewWithPayload(true),
    })
    if err != nil {
        return nil, err
    }
    hits := make([]program.Hit, 0, len(found))
    for _, point := range found {
        hits = append(hits, program.Hit{
            ID:       point.Id.GetUuid(),
            Source:   point.Payload["source"].GetStringValue(),
            Distance: float64(1 - point.Score), // cosine similarity to distance
        })
    }
    return hits, nil
}
```

Then wire it exactly like the shipped ones:

```go
cache, err := program.New(&qdrantStore{client: client}, embedder, program.Options{})
```

`program.NewMemoryStore()` is a complete implementation in about eighty lines if
you want a shorter reference — it is a map and a cosine loop.

## Bring your own caching behaviour

If the shipped policy is not what you want — you need a TTL, an LRU bound, a
shared cache across processes, or a rule about *what* may be cached — implement
`Cache` directly and skip `Store` entirely:

```go
type Cache interface {
    Lookup(ctx context.Context, key Key) (Entry, bool, error)
    Put(ctx context.Context, key Key, source string) error
    Forget(ctx context.Context, id string) error
    Close() error
}
```

The agent asks for nothing else. What "sufficiently similar" means is entirely
yours: exact text, embeddings, a normalised form, a hand-written rule.

The contract:

- A miss is `(Entry{}, false, nil)` — **not** an error.
- `Entry.ID` is opaque to the agent and comes back to you in `Forget`. Use
  whatever identifies the row.
- `Entry.Distance` is informational. Report zero if you do not rank.
- `Forget` on an unknown id is not an error.
- Must be safe for concurrent use.
- **Honour `Key.Scope()`.** Two keys may share a program only if their scopes are
  equal. Ignoring this leaks programs across roles and tenants.

### Example: Redis, shared across processes

```go
type redisCache struct {
    client *redis.Client
    ttl    time.Duration
}

func (cache *redisCache) key(key program.Key) string {
    return "codemode:" + key.Scope() + ":" +
        strings.ToLower(strings.TrimSpace(key.Request))
}

func (cache *redisCache) Lookup(ctx context.Context, key program.Key) (program.Entry, bool, error) {
    id := cache.key(key)
    source, err := cache.client.Get(ctx, id).Result()
    if errors.Is(err, redis.Nil) {
        return program.Entry{}, false, nil // a miss is not an error
    }
    if err != nil {
        return program.Entry{}, false, err
    }
    return program.Entry{ID: id, Source: source}, true, nil
}

func (cache *redisCache) Put(ctx context.Context, key program.Key, source string) error {
    return cache.client.Set(ctx, cache.key(key), source, cache.ttl).Err()
}

func (cache *redisCache) Forget(ctx context.Context, id string) error {
    return cache.client.Del(ctx, id).Err()
}

func (cache *redisCache) Close() error { return cache.client.Close() }
```

The TTL is the whole reason to write this rather than use the shipped ones: the
agent heals stale entries on failure, but a TTL also expires programs that still
*run* and have merely stopped being the best answer.

### Example: a policy about what may be cached

Reuse is safest for questions that only read. A program that writes is worth
regenerating each time, so an unnoticed change in intent cannot replay a mutation:

```go
// readOnlyCache caches only programs that call no write capability. It wraps
// any other Cache and narrows what reaches it.
type readOnlyCache struct {
    inner  program.Cache
    writes []string // capability names that change something
}

func (cache *readOnlyCache) Put(ctx context.Context, key program.Key, source string) error {
    for _, name := range cache.writes {
        if strings.Contains(source, name) {
            return nil // refuse to cache it; not an error
        }
    }
    return cache.inner.Put(ctx, key, source)
}

func (cache *readOnlyCache) Lookup(ctx context.Context, key program.Key) (program.Entry, bool, error) {
    return cache.inner.Lookup(ctx, key)
}
func (cache *readOnlyCache) Forget(ctx context.Context, id string) error { return cache.inner.Forget(ctx, id) }
func (cache *readOnlyCache) Close() error                               { return cache.inner.Close() }
```

Because it wraps rather than replaces, it composes with either shipped cache:

```go
cache := &readOnlyCache{
    inner:  program.NewExactCache(),
    writes: []string{"tasks.create", "tasks.delete", "tasks.update"},
}
```

The same shape gives you metrics, an allowlist of cacheable requests, or a kill
switch.

## Which layer to implement

| You want | Implement | You get for free |
| --- | --- | --- |
| A different vector database | `Store` | Embedding, threshold, scoping, healing |
| A different matching rule, TTL, or policy | `Cache` | Nothing — you own it |
| To narrow or observe an existing cache | `Cache`, wrapping another | Whatever you delegate |

Both are in package `program`. Neither requires a change anywhere else: the
agent depends on `Cache` and nothing more.

## Pre-warming

A cache only helps the second visitor. If you know the common questions — a
suggestions list, a dashboard's standard views — run them once at startup and the
first real visitor gets a hit:

```go
for _, question := range suggestions {
    if _, err := agent.Run(ctx, mode, model, question, options); err != nil {
        log.Warn("pre-warm failed", "question", question, "error", err)
    }
}
```

Do it per scope you care about. A program warmed for `admin` will not serve a
`member`, by design.

## See also

- [Latency](/latency) — measuring where a request's time actually goes, and
  what is not worth optimizing.
- [Permissions](/permissions) — why `CacheCaller` is not optional in a
  multi-role deployment.
- [Search](/search) — the capability index the semantic cache shares a database
  with.
