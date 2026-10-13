# Architecture

Code mode sits between a language model and your application. The model never
calls your API directly. It writes a short program; the program runs in a
sandbox; every call the program makes comes back to your Go code, which checks
it against an allowlist generated from your protobuf contracts before anything
runs.

```mermaid
flowchart LR
  user(["user request"]) --> agent
  subgraph host["your process (trusted)"]
    agent["agent.Run"]
    mode["codemode runtime<br/><small>allowlist + roles</small>"]
    execute["your ExecuteFunc<br/><small>auth, rows, database</small>"]
  end
  model[("LLM")]
  subgraph box["sandbox (untrusted)"]
    program["program<br/><small>written by the model</small>"]
  end
  agent -- "prompt: rules + matching signatures" --> model
  model -- "one program" --> agent
  agent -- "run" --> program
  program -- "codemode.execute(name, input)" --> mode
  mode -- "allowed: resolved RPC" --> execute
  execute -- "JSON" --> program
  program -- "final value" --> agent
  agent --> answer(["answer"])
```

Three ideas carry the whole design:

1. **One model turn, many calls.** The program loops and filters at machine
   speed. Intermediate results go back into the program, not into the model.
2. **The model chooses, the host decides.** The model can write anything. Only
   names in this caller's registry resolve, and only your `ExecuteFunc` touches
   data.
3. **Everything swappable is an interface.** Search store, model provider,
   sandbox language, where programs run, program cache: each is a port with
   adapters you pick by import and configuration.

## Two phases: build time and run time

```mermaid
flowchart TB
  subgraph build["build time (once)"]
    proto["your .proto files<br/><small>+ codemode annotations</small>"] -->|buf generate| gen["generated Go<br/><small>carries descriptors</small>"]
  end
  subgraph start["startup (once per process)"]
    gen -->|registry.FromFiles| full["full registry<br/><small>every capability, roles, schemas</small>"]
    full --> index["search<br/><small>Keyword or vector Index</small>"]
    runner["sandbox runner<br/><small>engine + isolation</small>"]
    model["llm.Client"]
  end
  subgraph request["per request"]
    identity["caller<br/><small>roles + permissions fetched from your app</small>"] --> narrowed["registry.For(caller)<br/><small>this caller's allowlist</small>"]
    full --> narrowed
    narrowed --> runtime["codemode.New(narrowed, search, execute)"]
    runtime --> run["agent.Run(ctx, mode, model, request, options)"]
  end
  index --> runtime
  runner --> run
  model --> run
```

- **Build time** turns contracts into Go. The generated package embeds its own
  descriptors, so nothing else has to be generated or kept in sync.
- **Startup** builds the expensive, shared things once: the full registry, the
  search index, the model client, the sandbox runner.
- **Per request** builds the cheap, caller-specific things: the narrowed
  registry and the runtime bound to it. That is a slice and a map; do it every
  time.

## Packages

```mermaid
flowchart BT
  capability["capability<br/><small>Capability, Registry, Match, Index, Keyword</small>"]
  llm["llm<br/><small>Client, Streamer</small>"]
  codemode["codemode<br/><small>CodeMode runtime, Recorder</small>"]
  registry["registry<br/><small>proto → Registry</small>"]
  sandbox["sandbox<br/><small>Runner, engines, isolations</small>"]
  program["program<br/><small>Cache, Store</small>"]
  agent["agent<br/><small>Run: the loop</small>"]
  mcpserver["mcpserver"]

  codemode --> capability
  registry --> capability
  program --> capability
  sandbox --> codemode
  agent --> codemode
  agent --> sandbox
  agent --> llm
  agent --> program
  mcpserver --> codemode
  mcpserver --> sandbox
```

Arrows point at what a package imports. Nothing at the bottom knows what sits
above it, and **none of these packages imports a third-party SDK**: importing
`codemode` alone links nothing outside the standard library and protobuf.
`scripts/check-consumer.sh` fails the build if that ever changes.

| Package | Job | You call |
| --- | --- | --- |
| `capability` | The vocabulary: a capability, a registry, a search match. Narrowing (`ForRole`, `Select`, `Require`), merging, keyword and vector search. | `For`, `Keyword`, `Merge` |
| `registry` | Reads protobuf descriptors and the codemode annotations into a `capability.Registry`. | `FromFiles` |
| `codemode` | The runtime the sandbox talks to. Resolves a capability name against the registry, calls `ExecuteFunc` with the RPC path, records calls. | `New` |
| `sandbox` | Runs a program and writes the prompt for its language. Engines and isolations register themselves. | `New`, `NewFromEnv` |
| `llm` | One interface over model providers. | `New`, `NewFromEnv` |
| `program` | Caches generated programs so a repeated request skips the model. | `NewExactCache`, `New` |
| `agent` | The loop: cache → search → prompt → model → sandbox → repair on error. | `Run`, `Answer` |
| `mcpserver` | Serves a runtime to MCP clients as two tools. | `New` |

## Ports and adapters

Each port is an interface in a parent package. Adapters live in child packages
and register themselves from `init`, so **your binary links only what you
blank-import**, and configuration picks among what is linked.

| Port | Adapters | Pick with |
| --- | --- | --- |
| `sandbox` engine | `sandbox/goja` (JavaScript), `sandbox/starlark`, `sandbox/lua` | `SANDBOX_ENGINE` |
| `sandbox` isolation | in-process (built in), `sandbox/subprocess` (`codemode-worker`, optionally in a container) | `SANDBOX_ISOLATION` |
| `llm.Client` | `llm/claude`, `llm/openai` (and any compatible endpoint) | `LLM_PROVIDER`, or whichever key is set |
| `capability.Store` (vector search) | `capability/sqlitestore`, `capability/pgstore` | `CAPABILITY_DB` |
| `program.Cache` | `program.NewExactCache`, `program.New` (semantic) | in code |
| `program.Store` | in memory, `sqlitestore`, `pgstore` | in code |
| capability source | protobuf (`registry`), MCP servers (`capability/mcpsource`) | in code |

```go
import (
	_ "github.com/mukezhz/codemode/llm/claude"         // links the Anthropic SDK
	_ "github.com/mukezhz/codemode/sandbox/goja"       // links goja
	_ "github.com/mukezhz/codemode/sandbox/subprocess" // links nothing new
)
```

Forget an import and `New` says exactly which line to add. See
[What your binary links](/dependencies).

## One request, end to end

```mermaid
sequenceDiagram
  autonumber
  participant App as your handler
  participant Agent as agent.Run
  participant Cache as program.Cache
  participant Mode as codemode runtime
  participant Model as llm.Client
  participant Runner as sandbox.Runner
  participant Exec as your ExecuteFunc

  App->>Agent: Run(ctx, mode, model, "how much did paid orders bring in?")
  Agent->>Cache: lookup(request, language, capability fingerprint)
  Cache-->>Agent: miss
  Agent->>Mode: Search(request, limit)
  Mode-->>Agent: matches, only from this caller's registry
  Agent->>Runner: Prompt(matches)
  Runner-->>Agent: language rules + signatures
  Agent->>Model: system prompt + request
  Model-->>Agent: program source
  Agent->>Runner: Run(ctx, mode, source)
  loop each codemode.execute in the program
    Runner->>Runner: call budget left?
    Runner->>Mode: Execute("orders.list", {"status":"paid"})
    Mode->>Mode: name in this caller's registry?
    Mode->>Exec: ("orders.v1.OrderService/List", input)
    Exec-->>Mode: JSON
    Mode-->>Runner: JSON, back into the program
  end
  Runner-->>Agent: final value
  Agent->>Cache: store program
  Agent-->>App: Result{Answer, Source, Calls, Timing, Usage}
```

- **Step 2** makes a repeated request cost milliseconds: the program, not the
  answer, is cached, and it re-runs against current data.
- **Steps 4 to 7**: the prompt contains only capabilities this caller may call,
  written in the sandbox's own language.
- **The loop** is where the work happens, with no model in it.
- **On error** (a typo, a wrong field), `agent.Run` gives the model the program
  and the error and asks again, up to `Options.Attempts`.

The full walk-through with the actual prompt and program is
[The flow, step by step](/flow).

## Where the boundaries are

```mermaid
flowchart LR
  subgraph trusted["trusted: your process"]
    direction TB
    reg["registry for this caller"]
    budgets["call budget, timeout,<br/>response size"]
    exec["ExecuteFunc:<br/>identity from ctx, row scope"]
  end
  subgraph untrusted["untrusted"]
    direction TB
    prompt_in["anything the model reads:<br/>user text, task titles, documents"]
    prog["the program"]
  end
  prompt_in -.steers.-> prog
  prog -->|"name + JSON only"| reg
  reg --> budgets --> exec
```

| Boundary | What enforces it | Lives in |
| --- | --- | --- |
| Which capabilities exist for this caller | `For(caller)`: the roles and permissions your app holds for them, against `(codemode.roles)` and `(codemode.permissions)` | your protos + your app |
| Whether a name resolves at all | a map lookup in the runtime | `codemode` |
| How many calls, how long, how large | call budget, timeout, response limit | `sandbox` options |
| Which rows a call may touch | the identity on `ctx`, never a field in the input | **your `ExecuteFunc`** |
| What the program can reach besides the bridge | the sandbox: no files, network, env; with `process` isolation, a separate process | `sandbox` |

The program is treated as hostile because anyone who can put text in front of
the model can steer it. Every check that matters runs on the trusted side, so a
program that escapes its interpreter still has nothing to call but the bridge.
See [Permissions](/permissions) and [`SECURITY.md`](https://github.com/mukezhz/codemode/blob/main/SECURITY.md).

## Where programs run

```mermaid
flowchart LR
  subgraph a["inprocess (default)"]
    a1["your process<br/>agent + goja + ExecuteFunc"]
  end
  subgraph b["process"]
    b1["your process<br/>agent + ExecuteFunc"] <-->|"JSON lines"| b2["codemode-worker<br/>goja"]
  end
  subgraph c["process, in a container"]
    c1["your process<br/>agent + ExecuteFunc"] <-->|"JSON lines"| c2["container (gVisor)<br/>codemode-worker"]
  end
```

| | Use for | Set |
| --- | --- | --- |
| `inprocess` | development, trusted internal users | nothing |
| `process` | end users of one organisation | `SANDBOX_ISOLATION=process` |
| `process` in a container | many tenants, untrusted input | plus `SANDBOX_WORKER="docker run ..."`, `SANDBOX_WORKER_CONTAINER=true` |

The language, the prompt and the program are identical in all three; only where
the interpreter runs changes. See [Running the worker](/worker).

## Other ways in and out

Code mode is a library first. Two packages connect it to the MCP ecosystem:

```mermaid
flowchart LR
  client["Claude Code, Cursor,<br/>any MCP client"] -->|"search_capabilities<br/>run_program"| server["mcpserver"]
  server --> mode["codemode runtime"]
  mode --> exec["your ExecuteFunc"]
  mode --> source["capability/mcpsource"]
  source -->|"tools/call"| ext["an MCP server<br/>(GitHub, ...)"]
```

- **[`mcpserver`](/mcp)**: your capabilities as two MCP tools. The client's model
  writes the program.
- **[`capability/mcpsource`](/capabilities#capabilities-from-an-mcp-server)**:
  another MCP server's tools as capabilities, so one program can call
  `github.listIssues` next to `orders.list`. Imported tools always require a role.

To see these pieces added to a real application, follow
[Adding it to an existing app](/existing-app), which walks a CRM through it.

## What you write, what you get

| You write | Code mode provides |
| --- | --- |
| protobuf services with a namespace and a description per RPC | the registry, the schemas, the prompt in three languages |
| `(codemode.roles)` on what is privileged | per-caller allowlists, and their absence from the prompt |
| `ExecuteFunc`: RPC path + JSON in, JSON out | the sandbox, the bridge, call budgets, recording, retries |
| which caller this is | search, caching, model calls, repair on error |

`ExecuteFunc` is the one piece nobody can write for you. It is also the only
place that touches your data, which is what keeps the rest swappable.

## Extending it

| To add | Implement | Then |
| --- | --- | --- |
| a sandbox language | `sandbox.Runner` | `sandbox.RegisterEngine` from `init`; add a row to `sandbox/enginetest` |
| a place to run programs | `sandbox.Runner` wrapping an engine | `sandbox.RegisterIsolation` from `init` |
| a model provider | `llm.Client` (and `llm.Streamer`) | register from `init`; add to `llm/providertest` |
| a vector store | `capability.Store` | register from `init` |
| a program cache | `program.Cache` or `program.Store` | pass it in `agent.Options` |
| a capability source | a function returning `capability.Registry` + `ExecuteFunc` | `capability.Merge` it into the registry |

Each adapter declares `var _ Port = (*impl)(nil)` beside its type, so
`git grep 'var _ sandbox.Runner'` lists every implementation.

## Decisions behind this

| Decision | Record |
| --- | --- |
| Adapters register themselves; parents never switch on a vendor | `notes/decisions/0002-pluggable-backends.md` |
| The library takes options; only `env.go` files read the environment | `notes/decisions/0003-explicit-configuration.md` |
| The registry is derived from compiled descriptors | `notes/decisions/0004-registry-from-descriptors.md` |
| Out-of-process sandbox over JSON lines | `notes/decisions/0005-subprocess-runner.md` |
| MCP server: two generic tools, the client writes the program | `notes/decisions/0006-mcp-server-shape.md` |
