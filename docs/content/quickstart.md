# Quickstart

Fifteen minutes from an empty directory to a model answering a question by
writing a program against your own API. Every file below is complete; copy them
as they are.

You will build a tiny shop with two capabilities, `orders.list` and
`orders.refund`, and ask: *"how much have paid orders brought in, and who is the
biggest customer?"*

```
shop/
├── buf.yaml
├── buf.gen.yaml
├── proto/
│   ├── codemode.proto          ← copied from the library, step 2
│   └── orders/v1/orders.proto  ← your contract, step 3
├── gen/                        ← generated, step 4
└── main.go                     ← your application, step 5
```

You need Go and [buf](https://buf.build/docs/installation). No database, no
Docker, and an API key only for the last step.

## 1. Create the module

```sh
mkdir shop && cd shop
go mod init example.com/shop
go get github.com/mukezhz/codemode@latest
```

## 2. Copy the annotations

Two protobuf options mark a service as something a model may call. The library
prints the file that matches its version:

```sh
mkdir -p proto/orders/v1
go run github.com/mukezhz/codemode/cmd/codemode-registry -print-proto > proto/codemode.proto
```

## 3. Write the contract

`proto/orders/v1/orders.proto`:

```protobuf
syntax = "proto3";

package orders.v1;

import "codemode.proto";

option go_package = "example.com/shop/gen/orders/v1;ordersv1";

service OrderService {
  option (codemode.namespace) = "orders";

  rpc List(ListOrdersRequest) returns (ListOrdersResponse) {
    option idempotency_level = NO_SIDE_EFFECTS;
    option (codemode.description) = "Lists orders, optionally only those with the given status: pending, paid or refunded.";
  }

  rpc Refund(RefundOrderRequest) returns (RefundOrderResponse) {
    option (codemode.description) = "Refunds a paid order by ID.";
    option (codemode.roles) = "support admin";
  }
}

message Order {
  int64 id = 1;
  string customer = 2;
  string status = 3;
  double total = 4;
}

message ListOrdersRequest {
  string status = 1;
}

message ListOrdersResponse {
  repeated Order orders = 1;
}

message RefundOrderRequest {
  int64 id = 1;
}

message RefundOrderResponse {
  Order order = 1;
}
```

What each line is for:

- `(codemode.namespace) = "orders"` + the method name gives the capability name
  the model calls: `orders.list`, `orders.refund`.
- `(codemode.description)` is the **only** prose the model reads about the call,
  and what search matches on. Write it for someone who has never seen your code.
- `(codemode.roles) = "support admin"`: only callers with one of those roles get
  `orders.refund` at all. See [Permissions](/permissions#several-roles-on-one-rpc-or-service).
- `idempotency_level = NO_SIDE_EFFECTS` marks a read. Everything else is treated
  as a write, which matters for [retries and plans](/writes).

## 4. Generate Go

`buf.yaml`:

```yaml
version: v2
modules:
  - path: proto
lint:
  use:
    - STANDARD
  except:
    - RPC_REQUEST_STANDARD_NAME
    - RPC_RESPONSE_STANDARD_NAME
    - RPC_REQUEST_RESPONSE_UNIQUE
  ignore:
    - proto/codemode.proto
```

`ignore` keeps `buf lint` off the library's file. The three exceptions are
optional: they let request and response types be named for what they hold
(`ListOrdersRequest`, a shared `Order`), which reads better in the prompt.

`buf.gen.yaml`:

```yaml
version: v2
inputs:
  - directory: proto
    exclude_paths:
      - proto/codemode.proto
plugins:
  - remote: buf.build/protocolbuffers/go:v1.36.11
    out: gen
    opt: paths=source_relative
```

```sh
buf generate
```

This writes `gen/orders/v1/orders.pb.go`. `codemode.proto` is excluded on
purpose: its Go code already ships inside the library, and your generated file
imports it from there.

## 5. Write the application

`main.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/registry"
	"github.com/mukezhz/codemode/sandbox"

	_ "github.com/mukezhz/codemode/llm/claude"
	_ "github.com/mukezhz/codemode/llm/openai"
	_ "github.com/mukezhz/codemode/sandbox/goja"
	_ "github.com/mukezhz/codemode/sandbox/subprocess"

	ordersv1 "example.com/shop/gen/orders/v1"
)

// execute is your application: the only code that touches real data.
func execute(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
	switch rpc {
	case "orders.v1.OrderService/List":
		var request struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(input, &request); err != nil {
			return nil, err
		}
		orders := []map[string]any{}
		for _, order := range []map[string]any{
			{"id": 1, "customer": "ada", "status": "paid", "total": 42.5},
			{"id": 2, "customer": "linus", "status": "pending", "total": 10},
			{"id": 3, "customer": "grace", "status": "paid", "total": 99},
		} {
			if request.Status == "" || order["status"] == request.Status {
				orders = append(orders, order)
			}
		}
		return json.Marshal(map[string]any{"orders": orders})
	}
	return nil, fmt.Errorf("RPC %q is not implemented", rpc)
}

func main() {
	ctx := context.Background()

	full, err := registry.FromFiles(ordersv1.File_orders_v1_orders_proto)
	if err != nil {
		log.Fatal(err)
	}
	allowed := full.ForRole("customer") // in your app: full.For(capability.Caller{...}) per request
	mode, err := codemode.New(allowed, capability.Keyword(allowed), execute)
	if err != nil {
		log.Fatal(err)
	}
	runner, err := sandbox.NewFromEnv(sandbox.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer runner.Close()

	model, err := llm.NewFromEnv()
	if err != nil && !errors.Is(err, llm.ErrNoProvider) {
		log.Fatal(err)
	}
	if model == nil {
		// No API key: run a hand-written program so you can see the sandbox work.
		value, err := runner.Run(ctx, mode, `
			const { orders } = codemode.execute("orders.list", { status: "paid" });
			const total = orders.reduce((sum, order) => sum + order.total, 0);
			return orders.length + " paid orders, " + total + " in total";`)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(value)
		return
	}

	request := "how much have paid orders brought in, and who is the biggest customer?"
	if len(os.Args) > 1 {
		request = os.Args[1]
	}
	result, err := agent.Run(ctx, mode, model, request, agent.Options{Runner: runner})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("program:\n%s\n\nanswer: %s\n", result.Source, agent.Answer(result.Answer))
}
```

The five things it does, in order:

| Line | What it is |
| --- | --- |
| `registry.FromFiles(...)` | reads the capabilities out of the generated code. Nothing to keep in sync. |
| `full.ForRole("customer")` | this caller's allowlist. `orders.refund` needs `support` or `admin`, so it is not in it. In a real app, pass what you fetch for the signed-in user: `full.For(capability.Caller{Roles: ..., Permissions: ...})`. See [Roles and permissions](/permissions#roles-and-permissions). |
| `capability.Keyword(allowed)` | search with no database. Switch to a [vector index](/search) later. |
| `execute` | **your code**: the only place that touches data. The program never sees it. |
| `agent.Run` | search → prompt → model writes a program → sandbox runs it → answer. |

## 6. Run it without a key

```sh
go mod tidy
go run .
```

```
2 paid orders, 141.5 in total
```

No model was involved: `main.go` ran a hand-written program so you can see the
sandbox, the registry and `execute` work before spending a token.

## 7. Let a model write the program

```sh
export ANTHROPIC_API_KEY=sk-ant-...
go run .
```

Or any OpenAI-compatible endpoint:

```sh
export OPENAI_API_KEY=sk-...
export OPENAI_BASE_URL=https://api.deepseek.com/v1   # omit for OpenAI
export OPENAI_MODEL=deepseek-chat
go run .
```

```
program:
const { orders } = codemode.execute("orders.list", { status: "paid" });
if (orders.length === 0) {
	return "no paid orders yet";
}
const revenue = orders.reduce((sum, order) => sum + order.total, 0);
const byCustomer = {};
for (const order of orders) {
	byCustomer[order.customer] = (byCustomer[order.customer] || 0) + order.total;
}
let topCustomer = null;
let topTotal = -Infinity;
for (const name of Object.keys(byCustomer)) {
	if (byCustomer[name] > topTotal) {
		topTotal = byCustomer[name];
		topCustomer = name;
	}
}
return "Paid orders brought in " + revenue + "; biggest customer is " + topCustomer + " (" + topTotal + ")";

answer: Paid orders brought in 141.5; biggest customer is grace (99)
```

The program differs a little from run to run; the answer should not.

One model call. The model wrote a program that fetched, filtered, summed and
ranked; only the final sentence came back. Ask something else:

```sh
go run . "list pending orders and their customers"
go run . "refund order 1"
```

The second one is worth running. The caller is a `customer`, so `orders.refund`
is not in their registry: not in the prompt, and not callable. The model works
with what it has:

```
answer: order 1: ada, 42.5, status paid (no refund capability is available, so nothing was changed)
```

Change `ForRole("customer")` to `ForRole("support")` and the same request
refunds, once you add a `Refund` case to `execute`.

## 8. Run programs out of process

So far every program ran inside your process, which is what you want while
developing. For production, run them in `codemode-worker`:

```sh
go get -tool github.com/mukezhz/codemode/cmd/codemode-worker   # pins it to your codemode version
go install github.com/mukezhz/codemode/cmd/codemode-worker     # puts it on PATH ($GOBIN)
SANDBOX_ISOLATION=process go run .
```

Nothing in `main.go` changes. `sandbox.NewFromEnv` reads the setting, starts a
worker once to check it works, and runs every program in a fresh child process
with a memory cap and no access to your environment. See
[Running the worker](/worker).

## Where next

| You want to | Read |
| --- | --- |
| add it to an app you already have (a CRM, say) | [Adding it to an existing app](/existing-app) |
| understand how the pieces fit | [Architecture](/architecture) |
| connect a real backend, gRPC or your services | [Add it to your application](/using-it#_3-write-executefunc) |
| scope data per user, roles, tenants | [Roles and tenants](/tenants) |
| configure the same binary for dev and prod | [Development and production](/environments) |
| expose it to Claude Code or Cursor | [Serving MCP clients](/mcp) |
