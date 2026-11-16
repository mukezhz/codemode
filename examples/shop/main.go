// shop is the quickstart as a runnable example: capabilities from typed Go
// functions, a typed answer, and a refund that waits for approval.
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

func newShop() *Shop {
	return &Shop{orders: []Order{
		{ID: 1, Customer: "ada", Status: "paid", Total: 42.5},
		{ID: 2, Customer: "linus", Status: "pending", Total: 10},
		{ID: 3, Customer: "grace", Status: "paid", Total: 99},
	}}
}

func (shop *Shop) Source() (codemode.Source, error) {
	return gosource.New(
		gosource.Func("orders.list", "Lists orders, optionally only those in one status.",
			gosource.Public(), shop.List, gosource.ReadOnly()),
		gosource.Func("orders.refund", "Refunds a paid order by ID.",
			gosource.Roles("support"), shop.Refund, gosource.NeedsApproval()),
	)
}

func main() {
	ctx := context.Background()
	source, err := newShop().Source()
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
