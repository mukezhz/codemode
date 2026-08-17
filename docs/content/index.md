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
      text: What code mode is
      link: /overview
    - theme: alt
      text: Add it to your application
      link: /using-it
    - theme: alt
      text: The flow, step by step
      link: /flow

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
      An RPC declares which roles may call it, beside the description a model
      reads. Narrowing a registry per caller withholds a capability from what
      runs and from the prompt at once — one decision, not two.
  - title: Generated from your contracts
    details: >
      Annotate a protobuf service with a namespace and a description per method.
      The registry, the schemas, and the model-facing declarations all fall out of
      make generate.
  - title: Three sandbox dialects
    details: >
      JavaScript, Starlark, or Lua — all pure Go, all with the same single binding.
      The engine writes its own prompt, so switching it switches the language the
      model is asked for and nothing else.
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
per-user scoping, and charts rendered from a prompt:

```sh
go run ./examples/taskreport -serve :8080
```

Start with [what code mode is](/overview), go straight to
[the flow](/flow) if you would rather see the mechanism first, or skip to
[adding it to your application](/using-it) if you already know what you want.
