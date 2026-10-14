---
layout: home

hero:
  name: Code Mode
  text: The model writes a program, not a call
  tagline: >
    Turn protobuf RPCs into capabilities a model can compose inside a sandbox.
    Fifty tasks become one model turn, and a registry decides what may actually run.
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
  - title: Generated from your contracts
    details: >
      Annotate a protobuf service with a namespace and a description per method.
      The registry, the schemas, and the model-facing declarations all fall out of
      make generate.
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

Five steps take an application from protobuf contracts to a model answering
questions against them. Only step 3 is code nobody can write for you.

```mermaid
flowchart LR
  a["1 annotate<br/><small>.proto</small>"] --> b["2 registry<br/><small>FromFiles</small>"]
  b --> c["3 execute<br/><small>your code</small>"]
  c --> d["4 per caller<br/><small>For(Caller)</small>"]
  d --> e["5 run<br/><small>agent.Run</small>"]
```

**1. Annotate the contract.** A namespace on the service, a description on each
method. That is all a model will ever read about it.

```proto
service OrderService {
  option (codemode.namespace) = "orders";
  rpc List(ListRequest) returns (ListResponse) {
    option (codemode.description) = "Lists orders, optionally filtered by status.";
  }
}
```

**2. Derive the registry** from the generated Go. No file to keep in sync.

```go
full, err := registry.FromFiles(ordersv1.File_orders_v1_orders_proto)
```

**3. Write `ExecuteFunc`.** It receives an RPC path and JSON input, and calls
your service layer the way your HTTP handlers already do.

```go
func execute(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
	switch rpc {
	case "orders.v1.OrderService/List":
		return listOrders(ctx, input) // identity comes from ctx, never from input
	}
	return nil, fmt.Errorf("RPC %q is not implemented", rpc)
}
```

**4. Narrow to the caller, per request.** What they may not call is withheld
from the prompt and refused at run time.

```go
allowed := full.For(capability.Caller{Roles: user.Roles, Permissions: user.Permissions})
mode, err := codemode.New(allowed, capability.Keyword(allowed), execute)
```

**5. Run a request.**

```go
result, err := agent.Run(ctx, mode, model, "how much have paid orders brought in?",
	agent.Options{Runner: runner})
fmt.Println(agent.Answer(result.Answer))
```

`model` comes from `llm.NewFromEnv()` and `runner` from
`sandbox.NewFromEnv(sandbox.Options{})`. The [Quickstart](/quickstart) builds all
of this as complete files in fifteen minutes; the [Integration guide](/using-it)
explains every choice; [Adding it to an existing app](/existing-app) walks a real
CRM through it.

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
