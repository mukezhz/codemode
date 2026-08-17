# Latency

A code mode request spends almost all of its time in one place, and it is not
the place people expect. This page shows how to measure that for yourself, what
the numbers usually look like, and the two things that actually change them.

## Where the time goes

Every request is measured. `agent.Result.Timing` breaks one down by stage:

```go
type Timing struct {
    Total    time.Duration // the whole call
    Cache    time.Duration // the program cache lookup
    Search   time.Duration // capability search
    Generate time.Duration // every model call, added up
    Run      time.Duration // every sandbox run, added up
}
```

`Timing.String()` renders it for a log line. A real request against a hosted
model looks like this:

```
total=5.754312s cache=2µs search=50µs generate=5.753459s run=733µs
```

Read that again. The model call is **99.985%** of the request. Search, the
sandbox, and the cache lookup together are 785µs — under a thousandth of the
total.

This ratio is not specific to one deployment. It follows from what the pieces
are: `Generate` is a network round trip to someone else's GPU, and `Run` is an
in-process interpreter evaluating twenty lines of JavaScript. They are separated
by three to four orders of magnitude, and no amount of tuning on your side
changes which one dominates.

Log it on every request:

```go
result, err := agent.Run(ctx, mode, model, request, options)
log.Info("answered", "cached", result.Cached, "timing", result.Timing.String())
```

The stages are stamped on every exit path, including failures. A request that
gave up after three attempts still reports what it paid for them, which is
exactly the request worth understanding.

## What not to optimize

Before changing anything, know what the fast parts cost. Measured on an M3 Pro:

| What | Cost |
| --- | --- |
| `goja.New()` | 269ns |
| `goja.New()` plus installing the host binding | 978ns |
| Compiling a 20-line program | 6.7µs |
| A whole `Runner.Run`, program and host call included | 79µs |

So the entire sandbox — creating an interpreter, installing the bridge, parsing,
and executing — is **79 microseconds**. Against a model call of five seconds it
is 0.0014% of the request.

Three optimizations get proposed regularly and none of them is worth doing:

**Pooling warm interpreters.** Saves the 978ns of construction and binding. That
is 0.00002% of a request, and it costs you isolation: a reused runtime keeps its
globals, so a program that assigns `foo = 1` without `let` leaves it there for
the next caller. A fresh interpreter per run is both faster to reason about and
free.

**Caching compiled programs.** Saves 6.7µs of parsing. Worth measuring only if
the program cache below is already in place and the model call has somehow
stopped being the bottleneck.

**Reducing Go↔JS serialization.** The bridge encodes the input to JSON and
decodes the output from it, because `codemode.CodeMode.Execute` is defined in
terms of `json.RawMessage` — the transport is protojson, not the interpreter.
Changing that means changing the interface every capability is executed through,
to save microseconds.

Measure first. If `Timing` says otherwise for your workload, believe your
numbers over this page.

## The one big win: don't make the call

The model call cannot be made much faster. It can be skipped entirely.

A program is a pure function of the request and the capabilities available to
answer it, so asking the same question twice pays twice for the same answer.
Cache the program and the second request runs the code the first one produced:

```
first ask   search + generate + run   ≈ 5.75s
second ask  cache  + run              ≈ 4ms
```

Two lines turns it on:

```go
cache := program.NewExactCache()
defer cache.Close()

result, err := agent.Run(ctx, mode, model, request, agent.Options{
    Runner: runner,
    Cache:  cache,
})
```

The program is cached, not the answer — it still runs, still calls into your
application, and still sees today's data. [Caching programs](/caching) covers
scoping it per role and tenant, the semantic cache that also hits across
rewordings, and how to plug in your own database or caching policy.

## Streaming: making the wait visible

The remaining seconds belong to the provider. Streaming does not reduce them. It
changes what they look like — a program appearing a line at a time instead of a
blank screen.

`llm.Streamer` is an optional interface a `Client` may also implement:

```go
type Streamer interface {
    CompleteStream(ctx context.Context, system, user string, chunk func(string)) (string, error)
}
```

Both shipped providers implement it. Ask for it through the agent:

```go
agent.Options{
    Runner:  runner,
    OnChunk: func(piece string) { /* push to the browser */ },
}
```

A model that cannot stream is not an error — `OnChunk` simply never fires and the
program arrives in one piece. Repair attempts stream too, so a caller displaying
the program should reset its buffer when `Result.Attempts` changes.

::: warning A wrapper must forward CompleteStream too
The same interface-narrowing trap as `Fingerprinter`. A traced or instrumented
`llm.Client` that does not implement `Streamer` turns streaming off for
everything behind it.
:::

### Time to first chunk

Streaming also gives you the number that says *why* a provider is slow. Log the
gap between sending the request and the first chunk:

```
took=5.753388s first_chunk=4.841s bytes=785
```

Two very different problems look identical without it:

- **High time to first chunk, fast tail** — as above: 4.8s of silence, then 785
  bytes in 0.9s. The provider is thinking, queuing, or emitting reasoning tokens
  your handler discards. Streaming barely helps a user here; the blank screen is
  merely shorter.
- **Low time to first chunk, slow tail** — output-bound. Streaming helps a lot,
  and so does asking for a smaller answer.

`examples/taskreport/trace` logs `first_chunk` next to `took`.

## Order of attack

1. **[Cache the program](/caching).** Removes the model call entirely for
   repeats. Start with `program.NewExactCache()`; move to the semantic cache when
   you have an embedder. This is the only change with an order-of-magnitude
   effect.
2. **Pre-warm.** Run your known common questions once at startup so the first
   real visitor gets a hit rather than a cold path.
3. **Stream.** Does not reduce latency; removes the blank screen. Cheap.
4. **Ask for less.** Model time scales with output tokens. A prompt that produces
   a leaner answer is directly faster, every time, for everyone.
5. **Trim the prompt.** Fewer capabilities described means fewer input tokens.
   Worth less than output, and it costs answer quality if search ranks poorly —
   see [Search](/search).
6. **Change model or provider.** The only thing that moves the floor.

Everything on your side of the wire is already under a millisecond. The wins are
all in not making the call, or making a smaller one.
