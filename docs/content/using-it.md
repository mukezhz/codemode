# Add it to your application

This page goes from an empty `main.go` to a working request, explaining each
choice along the way. For the shortest path, follow the [Quickstart](/quickstart)
first; for how the pieces fit, see [Architecture](/architecture); to add it beside
an app you already run, see [Adding it to an existing app](/existing-app). Every Go snippet
here compiles against the module as it stands.

## Install

```sh
go get github.com/mukezhz/codemode
```

Your binary links only what you blank-import. Importing `codemode` alone pulls
in **no third-party dependency at all**; each interpreter, model SDK, and
database driver arrives only when you ask for it. See
[what your binary links](/dependencies).

Nothing here pulls in gRPC — you own the transport, so you choose whether there
is one.

## What you supply

Four things. Two are generated, one is a few lines, and one is the only piece
nobody can write for you.

| You supply | How | Effort |
| --- | --- | --- |
| A registry | one call to `registry.FromFiles` | One line |
| A search function | `capability.Index.Search`, or a keyword loop | A few lines |
| An execute function | Dispatch an RPC path to your code | **Yours** |
| A model and a sandbox | `claude.New()` and `goja.New()`, or the `FromEnv` pair | Two calls |

`ExecuteFunc` is the one that matters. Everything else has a default; this is
where your application actually lives.

## 1. Annotate the contract

Two custom options mark a service as a capability surface. Write the options
file into your own buf module — `codemode-registry -print-proto` emits the
copy that matches your library version — and import it:

```protobuf
service TodoService {
  option (codemode.namespace) = "todos";

  rpc Complete(CompleteTodoRequest) returns (CompleteTodoResponse) {
    option (codemode.description) = "Marks a todo item as completed by ID.";
  }
}
```

The namespace plus the lower-cased method name becomes the capability name the
model calls: `todos.complete`. The description is the *only* thing search
matches on and the only prose the model sees, so write it for a reader who does
not have your code open.

::: warning
Streaming RPCs are rejected at generation time. A capability is one request and
one response, because that is what a synchronous call inside a sandbox can be.
:::

See [Capabilities](/capabilities) for the field-type rules.

## 2. Get the registry

The registry is the allowlist: capability name → RPC path, plus the schemas and
descriptions the model reads. There are two ways to get one, and the short one
is usually right.

### Derive it at startup (recommended)

`protoc-gen-go` puts the file descriptors into the package it generates. You
already import that package to implement the service, so everything needed to
describe it is linked into your binary:

```go
import (
	"github.com/mukezhz/codemode/registry"
	todov1 "myapp/gen/todo/v1"
)

capabilities, err := registry.FromFiles(todov1.File_todo_proto)
```

That is the whole step. **No descriptor set, no JSON file, no `go:embed`, no
extra build tool** — and the registry cannot drift from your contracts, because
it is derived from them every time the process starts.

`registry.FromGlobalFiles()` scans every protobuf file linked into the binary
instead. Only services carrying `(codemode.namespace)` contribute, so an
unrelated dependency cannot widen the registry — but naming the files is the
safer habit, because it says in code exactly which contracts are exposed.

### Generate a file (when you want it reviewable)

A checked-in `registry.json` makes a change to the capability surface show up as
a diff in a pull request. That is a real benefit for a surface a model calls, so
the command stays supported:

```sh
buf build --as-file-descriptor-set -o build/protoset.pb
go run github.com/mukezhz/codemode/cmd/codemode-registry@latest \
  -descriptor build/protoset.pb -out internal/capabilities/registry.json
```

Then load it — embedded, so the binary carries its own allowlist:

```go
//go:embed registry.json
var registryFS embed.FS

capabilities, err := capability.LoadFS(registryFS, "registry.json")
```

`Load`, `LoadFile`, and `LoadFS` reject unknown fields and an empty capability
list, so a hand-edited or truncated registry fails at startup rather than per
request.

::: tip Both paths produce identical registries
A test in this repository asserts the two are byte-identical, so a project can
start with `FromFiles` and add the generated file later — or run both, with CI
diffing the file against what reflection produces.
:::

### Which one

| | `registry.FromFiles` | `codemode-registry` |
| --- | --- | --- |
| Build steps | none | buf build, then generate |
| Can drift from protos | no | yes, until regenerated |
| Capability surface visible in review | no | yes, as a JSON diff |
| Needs generated Go | yes | no |

If you already run `buf generate`, start with `FromFiles`. Add the file when
someone wants the surface in code review.

## 3. Write ExecuteFunc

This is the trust boundary. It receives an RPC path that the runtime already
resolved from the registry — never a name the program invented — and the JSON
request. It returns the JSON response.

```go
type ExecuteFunc func(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error)
```

### Strategy A: a switch

Right when the capability surface is small or the implementations already live
in this process. No gRPC server, no network hop:

```go
func execute(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
	switch rpc {
	case "todo.v1.TodoService/List":
		return json.Marshal(map[string]any{"todos": []any{}})
	default:
		return nil, fmt.Errorf("RPC %q is not implemented", rpc)
	}
}
```

The cost is one case per capability. The `default` branch matters: a registry
entry with no case is a capability the model will be offered and cannot use.

### Strategy B: generic gRPC dispatch

Right when the capabilities are real services. One function handles every RPC,
present and future, because it works off the descriptor set rather than
generated stubs:

```go
import (
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/mukezhz/codemode/registry"
)

// grpcExecute dispatches any registry RPC over one connection. Files is the
// descriptor set your protos were built from; nothing here is generated per
// capability, so adding an RPC needs no Go change.
func grpcExecute(conn *grpc.ClientConn, files *protoregistry.Files) codemode.ExecuteFunc {
	return func(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
		serviceName, methodName, ok := strings.Cut(rpc, "/")
		if !ok {
			return nil, fmt.Errorf("malformed RPC path %q", rpc)
		}
		descriptor, err := files.FindDescriptorByName(protoreflect.FullName(serviceName))
		if err != nil {
			return nil, fmt.Errorf("service %s: %w", serviceName, err)
		}
		service, ok := descriptor.(protoreflect.ServiceDescriptor)
		if !ok {
			return nil, fmt.Errorf("%s is not a service", serviceName)
		}
		method := service.Methods().ByName(protoreflect.Name(methodName))
		if method == nil {
			return nil, fmt.Errorf("method %s not found on %s", methodName, serviceName)
		}

		request := dynamicpb.NewMessage(method.Input())
		if err := protojson.Unmarshal(input, request); err != nil {
			return nil, fmt.Errorf("decode request: %w", err)
		}

		response := dynamicpb.NewMessage(method.Output())
		if err := conn.Invoke(ctx, "/"+rpc, request, response); err != nil {
			return nil, err
		}
		return registry.MarshalJSON(response) // proto names, [] for empty lists, int64 as numbers
	}
}
```

Encode responses with `registry.MarshalJSON`, not `protojson.Marshal`. The model
is told field names like `due_date` and that `id` is a number; plain protojson
sends `dueDate`, quotes 64-bit integers, and leaves out empty lists, so a correct
program reads `undefined`.

Build `files` once at startup with `protodesc.NewFiles` over the same
`protoset.pb` the registry was generated from. Reusing that one artifact is what
keeps the two in step.

::: danger Your permissions still apply
`ExecuteFunc` runs with your application's full authority. The registry decides
which *capability* may run; it knows nothing about which *user* is asking. Carry
identity on the `ctx` and check it here, exactly as you would for a request that
arrived over HTTP. A program written by a model is untrusted input.
:::

## 4. Choose a search function

Search decides what the model is told exists. It never decides what may run —
see [the two lists](/flow#_4-the-model-writes-a-program).

**Start with keywords.** No database, no embedding calls, one line:

```go
search := capability.Keyword(capabilities)
```

It ranks capabilities by how many words of the request appear in their name and
description. Pass the same narrowed registry you give `codemode.New`.

**Move to semantic search when the registry outgrows the prompt.** Below roughly
twenty capabilities you can describe all of them every time and skip search
entirely. Above that, ranking starts to matter:

```go
import _ "github.com/mukezhz/codemode/capability/pgstore"   // or sqlitestore

store, err := capability.OpenStoreFromEnv(ctx)   // CAPABILITY_DB, CAPABILITY_DB_DSN
if err != nil {
	log.Fatal(err)
}
embedder, err := openai.NewEmbedder()   // llm/openai; the only embeddings protocol
if err != nil {
	log.Fatal(err)
}
embed := capability.Embedder{Model: embedder.Model, Embed: embedder.Embed}

// Rebuild when contracts or the embedding model change — not per request.
if err := capability.Rebuild(ctx, store, registry, embed); err != nil {
	log.Fatal(err)
}

index, err := capability.Open(ctx, store, embed)
if err != nil {
	log.Fatal(err)
}
defer index.Close()
```

`index.Search` already matches `codemode.SearchFunc`, so it goes straight into
`codemode.New`. `Open` fails loudly if the stored index was built with a
different embedding model, which is the failure you want: silently comparing
vectors from two models produces plausible nonsense.

See [Search](/search) for the backends.

## 5. Wire it together

```go
mode, err := codemode.New(registry, search, execute)
```

`codemode.New` validates the registry once — no blank names, no duplicates — and
returns the two-method runtime. `Execute` resolves a name to an RPC path through
that map, so an invented capability name fails a map lookup rather than a prompt
instruction.

## 6. Run a request

Importing an engine and a provider directly is the most explicit form and needs
no registration at all. Blank-import them instead if you want the choice made by
configuration — see [what your binary links](/dependencies).

```go
model, err := claude.New(claude.Options{Options: llm.Options{APIKey: config.Key}})
runner, err := goja.New(sandbox.Options{})
defer runner.Close()

result, err := agent.Run(ctx, mode, model, "finish anything about docs", agent.Options{
	Runner: runner,
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(agent.Answer(result.Answer))
```

`agent.Run` searches, builds the prompt in the runner's dialect, asks the model
once, and evaluates the program. If the program fails, it hands the model the
source and the error and asks again, up to `Attempts` times.

`Result` carries more than the answer — `Source`, `Matches`, and `Attempts` are
what you log when something goes wrong, and `Timing` is what you log when it goes
slowly.

### Answer repeats without paying for them again

The model call is typically 99.9% of a request. A program is a pure function of
the request and the capabilities available, so a repeat need not buy one twice:

```go
cache := program.NewExactCache()
defer cache.Close()

result, err := agent.Run(ctx, mode, model, request, agent.Options{
	Runner:      runner,
	Cache:       cache,
	CacheCaller: string(identity.Role), // never share across roles or tenants
})
```

Same question, second time: about four milliseconds instead of several seconds.
The cached program still runs and still sees live data.

`Cache` is an interface, so your own database or caching policy drops in without
touching anything else — [Caching programs](/caching) covers that, the semantic
cache that also hits across rewordings, and how to scope entries safely.

### Show the program as it is written

For an interactive frontend, stream it:

```go
agent.Options{
	Runner:  runner,
	OnChunk: func(piece string) { /* push to the browser */ },
}
```

This does not make the answer arrive sooner; it fills the several seconds a model
takes with something to look at. See [Models](/models#streaming).

To pin the model, bound the response, or add domain guidance:

```go
model, err := claude.New(claude.Options{Options: llm.Options{
	APIKey:    config.Key,
	Model:     "claude-sonnet-5",
	MaxTokens: 8000,
}})

result, err := agent.Run(ctx, mode, model, request, agent.Options{
	Runner:       runner,
	Instructions: "Answer in one sentence. Never create a todo without a title.",
})
```

`Instructions` is appended to the engine's system prompt, not a replacement for
it — see [configuring the model](/models#configuring-the-model).

::: tip The library never reads the environment
Every constructor takes explicit options, so your application's own
configuration — flags, a config file, a mounted secret — is the only source of
truth. If you *want* the environment read, ask for it by name:
`llm.OptionsFromEnv()`, `sandbox.NewFromEnv()`, `capability.OpenStoreFromEnv()`.
Only a name containing `FromEnv` touches it, and a check in CI enforces that.
:::

::: tip Pass your own runner
`agent.Options{Runner: nil}` builds a runner per request and closes it. Building
one at startup and passing it in avoids that, and a `Runner` is safe to share:
each `Run` creates a fresh interpreter, so programs cannot see each other.
:::

## The whole thing

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/llm/claude"
	"github.com/mukezhz/codemode/registry"
	"github.com/mukezhz/codemode/sandbox"
	"github.com/mukezhz/codemode/sandbox/goja"

	todov1 "myapp/gen/todo/v1"
)

func main() {
	ctx := context.Background()

	// 1. The registry: what may run. Derived from the contracts, so there is
	// no file to build, embed, or keep in step.
	capabilities, err := registry.FromFiles(todov1.File_todo_proto)
	if err != nil {
		log.Fatal(err)
	}

	// 2. Search: what the model gets told about.
	search := capability.Keyword(capabilities)

	// 3. The runtime: name -> RPC, then your transport.
	mode, err := codemode.New(capabilities, search, execute)
	if err != nil {
		log.Fatal(err)
	}

	// 4. Model and sandbox, configured by your application. Nothing here
	// reads the environment.
	model, err := claude.New(claude.Options{
		Options: llm.Options{APIKey: os.Getenv("ANTHROPIC_API_KEY")}, // or your own config
	})
	if err != nil {
		log.Fatal(err)
	}
	runner, err := goja.New(sandbox.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer runner.Close()

	// 5. One request, one model turn.
	result, err := agent.Run(ctx, mode, model, "finish anything about docs", agent.Options{Runner: runner})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(agent.Answer(result.Answer))
}

func execute(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
	switch rpc {
	case "todo.v1.TodoService/List":
		return json.Marshal(map[string]any{"todos": []any{}})
	default:
		return nil, fmt.Errorf("RPC %q is not implemented", rpc)
	}
}
```

Set `ANTHROPIC_API_KEY` and run it. [`examples/todo`](https://github.com/mukezhz/codemode/tree/main/examples/todo)
is the same shape with a real in-memory application behind `execute`.

## Driving it yourself

`agent.Run` is a convenience, not the API. Skip it when you want the prompt in
your own conversation, a different repair policy, or a program you wrote by
hand:

```go
matches, err := mode.Search(ctx, request, 5)
prompt := runner.Prompt(matches)        // the dialect writes its own instructions
source := agent.Program(response)       // strips markdown fences
value, err := runner.Run(ctx, mode, source)
```

`runner.Prompt` is the piece to keep. The engine that evaluates the program
writes the instructions for producing it, so switching `SANDBOX_ENGINE` switches
the language the model is asked for and nothing else. A prompt you write by hand
goes stale the moment someone changes engines.

## Behind an HTTP handler

Build the expensive things once and share them; keep the per-request things per
request.

| Build once, at startup | Build per request |
| --- | --- |
| `registry`, `mode`, `index`, `model`, `runner` | The `ctx`, its deadline, and its identity |

All of the startup values are safe for concurrent use. The one thing that is
not automatically safe is your own `ExecuteFunc` — it is ordinary Go called from
many goroutines, so whatever state it touches needs the locking it would need
anyway.

```go
func handler(mode codemode.CodeMode, model llm.Client, runner sandbox.Runner) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), 60*time.Second)
		defer cancel()

		result, err := agent.Run(ctx, mode, model, request.URL.Query().Get("q"), agent.Options{Runner: runner})
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(writer).Encode(map[string]any{"answer": result.Answer})
	}
}
```

Give the request a deadline well above the sandbox's own `SANDBOX_TIMEOUT`. The
sandbox bounds one program; the handler bounds search, the model call, the
program, and any repair attempts after it.

## Before production

- **Check permissions inside `ExecuteFunc`.** The registry is not an authorization system.
- **Set `SANDBOX_MAX_CALLS` and `SANDBOX_TIMEOUT` deliberately.** The defaults — 50 calls, 30 seconds — are demo numbers.
- **Log `Result.Source`.** When an answer is wrong, the program is the evidence.
- **Regenerate the registry in CI.** A drifted `registry.json` is a silent failure.
- **Run programs out of process** if they will ever be shaped by untrusted end users: `SANDBOX_ISOLATION=process`, and a container worker for tenants you do not trust. See [Development and production](/environments).
- **Blank-import exactly what you run.** An unregistered backend fails at `New`, not at startup — see [what your binary links](/dependencies).

Then [Development and production](/environments) for configuring the same binary for both,
[Configuration](/configuration) for every environment variable, and
[Troubleshooting](/troubleshooting) for what the errors mean.
