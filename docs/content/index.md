---
layout: home

hero:
  name: Code Mode
  text: The model writes a program, not a call
  tagline: >
    Turn your typed Go functions, protobuf RPCs or MCP tools into capabilities a
    model composes inside a sandbox. Fifty tasks become one model turn, and a
    registry decides what may actually run.
  actions:
    - theme: brand
      text: Quickstart
      link: /quickstart
    - theme: alt
      text: Integration guide
      link: /using-it
    - theme: alt
      text: Add it to an existing app
      link: /existing-app

features:
  - title: One turn, not fifty
    details: >
      The program loops and filters at machine speed inside the sandbox. Only the
      final answer returns, so intermediate results never re-enter the model's
      context.
  - title: A registry decides what runs
    details: >
      Search shapes what the model knows about. The generated registry shapes what
      may execute. Two separate lists, so an invented capability name is rejected
      by a map lookup rather than by a prompt.
  - title: Permissions live in the contract
    details: >
      An RPC declares which roles or permissions may call it. Each request, pass
      what the caller holds, fixed roles or ones an admin created at runtime, and
      the capability is withheld from what runs and from the prompt at once.
  - title: Typed at both ends
    details: >
      The schema comes from your Go structs, protos or JSON Schema. Every call is
      validated before your handler runs, and agent.RunAs returns the answer as
      your struct. A mismatch goes back to the model to fix.
  - title: A person approves the risky writes
    details: >
      Mark a write NeedsApproval and the program records it instead of running it.
      Show the plan, apply it by ID once, and the client never touches what was
      approved.
  - title: In-process to container, one setting
    details: >
      JavaScript, Starlark, or Lua, run in your process while you develop and in a
      separate worker or a container per run in production. SANDBOX_ISOLATION
      changes where; the program and the prompt stay the same.
  - title: Swap the pieces, not the code
    details: >
      Search runs on sqlite-vector or pgvector. Models come from Claude, OpenAI, or
      anything speaking either protocol. Each is an interface chosen by one
      environment variable.
  - title: Repeats cost milliseconds
    details: >
      A program is a pure function of the request and the capabilities available,
      so the second identical question skips the model entirely — about four
      milliseconds instead of several seconds. Cache is an interface; bring your
      own store or policy.
  - title: Your application is untouched
    details: >
      It validates its own input, enforces its own permissions, and owns what the
      operation means. The sandbox never sees an RPC path, a connection string, or
      a token.
---

## Use it in your app

Three steps take the Go functions you already have to a model answering
questions with them.

```mermaid
flowchart LR
  a["1 define<br/><small>gosource.Func</small>"] --> b["2 per caller<br/><small>FromSource</small>"]
  b --> c["3 run<br/><small>agent.RunAs</small>"]
```

**1. Define capabilities from typed functions.** The structs are the schema;
access is required, and writes can wait for a person.

```go
source, err := gosource.New(
	gosource.Func("orders.list", "Lists orders, optionally only those in one status.",
		gosource.Public(), shop.List, gosource.ReadOnly()),
	gosource.Func("orders.refund", "Refunds a paid order by ID.",
		gosource.Roles("support"), shop.Refund, gosource.NeedsApproval()),
)
```

**2. Narrow to the caller, per request.** What they may not call is withheld
from the prompt and refused at run time.

```go
mode, err := codemode.FromSource(source, capability.Caller{Roles: user.Roles})
```

**3. Run a request, and get a struct back.**

```go
revenue, result, err := agent.RunAs[Revenue](ctx, mode, model,
	"how much have paid orders brought in?", agent.Options{Runner: runner})
```

`model` comes from `llm.NewFromEnv()` and `runner` from
`sandbox.NewFromEnv(sandbox.Options{})`. The [Quickstart](/quickstart) builds all
of this as complete files in ten minutes. Already have protos or MCP servers?
See [Capability sources](/sources).

## In one screen

```mermaid
flowchart LR
  req["user request"] --> search["search"]
  search --> prompt["prompt"] --> model["model"] --> program["program"]
  program --> sandbox["sandbox"]
  sandbox -->|"execute(name, input)"| allow["allowlist"]
  allow -->|resolved RPC path| app["your application"]
  app -->|JSON| sandbox
  sandbox --> answer["answer"]
```

The model is asked once. Everything between the first call and the last happens
inside the sandbox, and every capability call is checked against the registry
before it reaches your code.

## Try it without configuring anything

With no API keys and no database, the whole path still runs — search falls back
to keyword ranking and a fixed program stands in for the model.

```sh
mise install
make demo-todo
```

Then give a model the pen:

```sh
export ANTHROPIC_API_KEY=...
go run ./examples/todo -request "finish anything about docs"
```

Or run the complete one — a task tracker with a web UI, SQLite storage,
per-user scoping, and charts rendered from a prompt. It talks to any
OpenAI-compatible endpoint:

```sh
go run ./examples/taskreport -serve :8080 \
  -base-url https://api.deepseek.com/v1 -api-key sk-... -model deepseek-chat
```

Read [what code mode is](/overview) for the idea, [Architecture](/architecture)
for how the pieces fit, or [the flow](/flow) for one request traced step by step.
