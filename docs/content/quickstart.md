# Quickstart

Ten minutes from an empty directory to a model answering a question by writing a
program against your own Go functions. No protobuf, no code generation, no
`switch` over RPC names: the functions you already have, typed, are the
capabilities.

You will build a tiny shop with two capabilities, `orders.list` and
`orders.refund`, and ask: *"how much have paid orders brought in, and who is the
biggest customer?"* You will get the answer back as a Go struct, and the refund
will wait for you to approve it.

You need Go. No database, no Docker, and an API key only for step 4. The
finished code is
[`examples/shop`](https://github.com/mukezhz/codemode/tree/main/examples/shop)
in the repository, with tests.

## 1. Create the module

```sh
mkdir shop && cd shop
go mod init example.com/shop
go get github.com/mukezhz/codemode@latest
```

## 2. Write the application

`main.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/capability/gosource"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/sandbox"

	_ "github.com/mukezhz/codemode/llm/claude"
	_ "github.com/mukezhz/codemode/llm/openai"
	_ "github.com/mukezhz/codemode/sandbox/goja"
	_ "github.com/mukezhz/codemode/sandbox/subprocess"
)

type Status string

func (Status) Enum() []string { return []string{"pending", "paid", "refunded"} }

type Order struct {
	ID       int64   `json:"id"`
	Customer string  `json:"customer"`
	Status   Status  `json:"status"`
	Total    float64 `json:"total"`
}

type ListOrders struct {
	Status Status `json:"status,omitempty" desc:"only orders in this status; leave out for all"`
}

type Orders struct {
	Orders []Order `json:"orders"`
}

type RefundOrder struct {
	ID int64 `json:"id" desc:"the id from orders.list"`
}

type OrderResult struct {
	Order Order `json:"order"`
}

// Shop is your application: the only code that touches real data.
type Shop struct {
	orders []Order
}

func (shop *Shop) List(_ context.Context, request ListOrders) (Orders, error) {
	var found []Order
	for _, order := range shop.orders {
		if request.Status == "" || order.Status == request.Status {
			found = append(found, order)
		}
	}
	return Orders{Orders: found}, nil
}

func (shop *Shop) Refund(_ context.Context, request RefundOrder) (OrderResult, error) {
	for index := range shop.orders {
		if shop.orders[index].ID == request.ID && shop.orders[index].Status == "paid" {
			shop.orders[index].Status = "refunded"
			return OrderResult{Order: shop.orders[index]}, nil
		}
	}
	return OrderResult{}, fmt.Errorf("no paid order %d", request.ID)
}

type Revenue struct {
	Total       float64 `json:"total"`
	TopCustomer string  `json:"top_customer"`
}

func main() {
	ctx := context.Background()
	shop := &Shop{orders: []Order{
		{ID: 1, Customer: "ada", Status: "paid", Total: 42.5},
		{ID: 2, Customer: "linus", Status: "pending", Total: 10},
		{ID: 3, Customer: "grace", Status: "paid", Total: 99},
	}}

	source, err := gosource.New(
		gosource.Func("orders.list", "Lists orders, optionally only those in one status.",
			gosource.Public(), shop.List, gosource.ReadOnly()),
		gosource.Func("orders.refund", "Refunds a paid order by ID.",
			gosource.Roles("support"), shop.Refund, gosource.NeedsApproval()),
	)
	if err != nil {
		log.Fatal(err)
	}
	caller := capability.Caller{Roles: []string{"support"}} // in your app: the signed-in user's roles
	mode, err := codemode.FromSource(source, caller)
	if err != nil {
		log.Fatal(err)
	}
	runner, err := sandbox.NewFromEnv(sandbox.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer runner.Close()

	model, err := llm.NewFromEnv()
	if errors.Is(err, llm.ErrNoProvider) {
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
	if err != nil {
		log.Fatal(err)
	}

	if len(os.Args) > 1 {
		refund(ctx, mode, model, runner, os.Args[1])
		return
	}
	revenue, result, err := agent.RunAs[Revenue](ctx, mode, model,
		"how much have paid orders brought in, and who is the biggest customer?", agent.Options{Runner: runner})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("program:\n%s\n\n%.2f in total, biggest customer %s\n", result.Source, revenue.Total, revenue.TopCustomer)
}

func refund(ctx context.Context, mode codemode.CodeMode, model llm.Client, runner sandbox.Runner, request string) {
	plans := agent.NewPlans(agent.NewMemoryPlanStore(), agent.PlanOptions{})
	_, err := agent.Run(ctx, mode, model, request, agent.Options{Runner: runner, Plans: plans, PlanOwner: "you"})
	var pending *agent.Pending
	if !errors.As(err, &pending) {
		fmt.Println("nothing needed approval:", err)
		return
	}
	for _, call := range pending.Calls {
		fmt.Printf("wants to run %s %s\n", call.Name, call.Input)
	}
	fmt.Print("approve? [y/N] ")
	var answer string
	fmt.Scanln(&answer)
	if answer != "y" {
		plans.Reject(ctx, "you", pending.ID, "declined at the prompt")
		fmt.Println("nothing changed")
		return
	}
	plan, err := plans.Approve(ctx, mode, "you", pending.ID)
	if err != nil {
		log.Fatal(err) // names the plan and the call that failed
	}
	fmt.Printf("ran %d call(s)\n", len(plan.Applied))
}
```

What it does, in order:

| Code | What it is |
| --- | --- |
| `type ListOrders struct`, `type Orders struct` | the contract. The model sees these shapes; `desc` tags are the prose it reads about a field. Fields follow `encoding/json`: `omitempty` or a pointer makes one optional, every other field is required. |
| `func (Status) Enum()` | a string type that accepts only these values, in the prompt and at the boundary. |
| `gosource.Func(name, description, access, handler)` | one capability. The description is what search matches and the model reads. |
| `gosource.Public()`, `gosource.Roles("support")` | who may call it. There is no default: a capability with no access does not compile into a source. |
| `gosource.ReadOnly()` | a read. Everything else is a write: never cached, never retried after it ran. |
| `gosource.NeedsApproval()` | the write waits for a person. See [Approving writes](/approval). |
| `codemode.FromSource(source, caller)` | this caller's runtime: only what they may call, searched by keyword. |
| `agent.RunAs[Revenue]` | search → prompt → model writes a program → sandbox runs it → the value is checked against `Revenue` and decoded. |

Before your function runs, every call the program makes is checked against the
shape: a wrong type, an unknown field or a missing required field goes back to
the model as an error naming the field, and the model fixes the program. Your
handler only ever sees a valid `ListOrders`. See [Types and validation](/types).

## 3. Run it without a key

```sh
go mod tidy
go run .
```

```
2 paid orders, 141.5 in total
```

No model was involved: `main.go` ran a hand-written program so you can see the
sandbox, the capabilities and your functions work before spending a token.

## 4. Let a model write the program

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
const byCustomer = {};
let total = 0;
for (const order of orders) {
	total += order.total;
	byCustomer[order.customer] = (byCustomer[order.customer] || 0) + order.total;
}
const top = Object.keys(byCustomer).sort((a, b) => byCustomer[b] - byCustomer[a])[0];
return { total: total, top_customer: top };

141.50 in total, biggest customer grace
```

The program differs a little from run to run; the answer should not.

One model call. The model was told the shape of `Revenue`, so it returned an
object instead of a sentence; had it returned the wrong shape, it would have
been asked to fix it.

## 5. Approve a write

```sh
go run . "refund order 1"
```

```
wants to run orders.refund {"id":1}
approve? [y/N] y
ran 1 call(s)
```

`orders.refund` needs approval, so the program ran its reads and *recorded* the
refund instead of making it. `agent.Run` returned `*agent.Pending`; the plan was
kept under an ID with an expiry, and `plans.Approve` ran it once you said yes.
Answer anything but `y` and `plans.Reject` discards it. Plans here live in
memory; [Approving writes](/approval) keeps them in your database so the answer
can come tomorrow.

Change the caller to `capability.Caller{Roles: []string{"customer"}}` and
`orders.refund` disappears: not in the prompt, and not callable.

## 6. Run programs out of process

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
| use protos, MCP servers or several sources at once | [Capability sources](/sources) |
| hold writes for a person | [Approving writes](/approval) |
| know exactly what is checked | [Types and validation](/types) |
| add it to an app you already have (a CRM, say) | [Adding it to an existing app](/existing-app) |
| scope data per user, roles, tenants | [Roles and tenants](/tenants) |
| expose it to Claude Code or Cursor | [Serving MCP clients](/mcp) |
