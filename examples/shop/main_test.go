package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/codemode/sourcetest"
	"github.com/mukezhz/codemode/sandbox"
)

type script struct{ programs []string }

func (script *script) Name() string { return "test/script" }

func (script *script) Complete(context.Context, string, string) (string, error) {
	if len(script.programs) == 0 {
		return "", errors.New("no programs left")
	}
	program := script.programs[0]
	script.programs = script.programs[1:]
	return program, nil
}

func setup(t *testing.T, roles ...string) (*Shop, codemode.CodeMode, sandbox.Runner) {
	t.Helper()
	shop := newShop()
	source, err := shop.Source()
	if err != nil {
		t.Fatal(err)
	}
	sourcetest.Run(t, source)
	mode, err := codemode.FromSource(source, capability.Caller{Roles: roles})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := sandbox.New(sandbox.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runner.Close() })
	return shop, mode, runner
}

func TestRevenueComesBackTyped(t *testing.T) {
	_, mode, runner := setup(t, "support")
	model := &script{programs: []string{
		`return "141.5, grace";`,
		`const { orders } = codemode.execute("orders.list", { status: "paid" });
		 const top = orders.reduce((a, b) => (b.total > a.total ? b : a));
		 return { total: orders.reduce((sum, o) => sum + o.total, 0), top_customer: top.customer };`,
	}}
	revenue, result, err := agent.RunAs[Revenue](context.Background(), mode, model, "revenue?", agent.Options{Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if revenue.Total != 141.5 || revenue.TopCustomer != "grace" || result.Attempts != 2 {
		t.Fatalf("revenue = %+v after %d attempts", revenue, result.Attempts)
	}
}

func TestARefundWaitsForApproval(t *testing.T) {
	shop, mode, runner := setup(t, "support")
	plans := agent.NewPlans(agent.NewMemoryPlanStore(), agent.PlanOptions{})
	model := &script{programs: []string{`codemode.execute("orders.refund", { id: 1 }); return "refunded";`}}

	_, err := agent.Run(context.Background(), mode, model, "refund order 1", agent.Options{Runner: runner, Plans: plans, PlanOwner: "you"})
	var pending *agent.Pending
	if !errors.As(err, &pending) || shop.orders[0].Status != "paid" {
		t.Fatalf("err = %v, status = %s; the refund must wait", err, shop.orders[0].Status)
	}
	if _, err := plans.Approve(context.Background(), mode, "you", pending.ID); err != nil {
		t.Fatal(err)
	}
	if shop.orders[0].Status != "refunded" {
		t.Fatalf("status = %s after approval", shop.orders[0].Status)
	}
}

func TestACustomerCannotRefund(t *testing.T) {
	_, mode, runner := setup(t, "customer")
	_, err := runner.Run(context.Background(), mode, `return codemode.execute("orders.refund", { id: 1 });`)
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("err = %v", err)
	}
}

func TestAnInvalidStatusNeverReachesTheShop(t *testing.T) {
	_, mode, runner := setup(t)
	_, err := runner.Run(context.Background(), mode, `return codemode.execute("orders.list", { status: "lost" });`)
	if err == nil || !strings.Contains(err.Error(), "want one of pending, paid, refunded") {
		t.Fatalf("err = %v", err)
	}
}
