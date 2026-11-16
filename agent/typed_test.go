package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
)

type summary struct {
	Open  int      `json:"open" desc:"todos not done"`
	Names []string `json:"names"`
	Note  string   `json:"note,omitempty"`
}

func TestRunAsRepairsAnAnswerOfTheWrongShape(t *testing.T) {
	mode, _ := effects(t)
	author := &model{responses: []string{
		`return "two open";`,
		`return { open: 1.5, names: [] };`,
		`const { todos } = codemode.execute("todos.list", {}); return { open: todos.length, names: ["a"] };`,
	}}

	answer, result, err := RunAs[summary](context.Background(), mode, author, "summarize", Options{Runner: newRunner(t)})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Open != 1 || strings.Join(answer.Names, ",") != "a" || result.Attempts != 3 {
		t.Fatalf("answer = %+v, attempts = %d", answer, result.Attempts)
	}
	if !strings.Contains(author.systems[0], "summary { names: repeated string, note?: string, open: integer (todos not done) }") {
		t.Fatalf("system prompt does not describe the shape:\n%s", author.systems[0])
	}
	if !strings.Contains(author.prompts[1], "want object summary") || !strings.Contains(author.prompts[2], "open: want integer, got 1.5") {
		t.Fatalf("repair prompts = %q", author.prompts[1:])
	}
}

func TestRunAsGivesUpWithTheLastShapeError(t *testing.T) {
	mode, _ := effects(t)
	author := &model{responses: []string{`return {};`, `return {};`}}
	_, _, err := RunAs[summary](context.Background(), mode, author, "summarize", Options{Runner: newRunner(t), Attempts: 2})
	if err == nil || !strings.Contains(err.Error(), "names: required") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunAsString(t *testing.T) {
	mode, _ := effects(t)
	author := &model{responses: []string{`return 3;`, `return "three";`}}
	answer, _, err := RunAs[string](context.Background(), mode, author, "count", Options{Runner: newRunner(t)})
	if err != nil || answer != "three" {
		t.Fatalf("answer = %q, err = %v", answer, err)
	}
}

func approvals(t *testing.T) (codemode.CodeMode, *[]string) {
	t.Helper()
	var executed []string
	registry := capability.Registry{Capabilities: []capability.Capability{
		{Name: "todos.list", RPC: "list", ReadOnly: true},
		{Name: "todos.rename", RPC: "rename"},
		{Name: "todos.delete", RPC: "delete", NeedsApproval: true},
	}}
	source := codemode.Source{Registry: registry, Execute: func(_ context.Context, rpc string, _ json.RawMessage) (json.RawMessage, error) {
		executed = append(executed, rpc)
		return json.RawMessage(`{"todos":[{"id":1}]}`), nil
	}}
	mode, err := codemode.FromSource(source, capability.Caller{})
	if err != nil {
		t.Fatal(err)
	}
	return mode, &executed
}

func TestRunHoldsAWriteThatNeedsApprovalUntilThePlanIsApplied(t *testing.T) {
	mode, executed := approvals(t)
	author := &model{responses: []string{`
		const { todos } = codemode.execute("todos.list", {});
		codemode.execute("todos.rename", { id: 1 });
		codemode.execute("todos.delete", { id: todos[0].id });
		codemode.execute("todos.rename", { id: 2 });
		return "cleaned up";`}}
	plans := NewMemoryPlans(0)

	_, err := Run(context.Background(), mode, author, "clean up", Options{Runner: newRunner(t), Plans: plans, PlanOwner: "ada"})
	var held *Pending
	if !errors.As(err, &held) {
		t.Fatalf("err = %v, want *Pending", err)
	}
	if strings.Join(*executed, ",") != "list,rename" || len(held.Calls) != 2 || held.ID == "" {
		t.Fatalf("executed = %v, held = %+v", *executed, held)
	}

	if _, err := ApplyPlan(context.Background(), mode, plans, "grace", held.ID); !errors.Is(err, ErrNoPlan) {
		t.Fatalf("another owner applied the plan: %v", err)
	}
	calls, err := ApplyPlan(context.Background(), mode, plans, "ada", held.ID)
	if err != nil || len(calls) != 2 || strings.Join(*executed, ",") != "list,rename,delete,rename" {
		t.Fatalf("calls = %+v, executed = %v, err = %v", calls, *executed, err)
	}
	if _, err := ApplyPlan(context.Background(), mode, plans, "ada", held.ID); !errors.Is(err, ErrNoPlan) {
		t.Fatalf("a plan applied twice: %v", err)
	}
}

func TestAnExpiredPlanDoesNotApply(t *testing.T) {
	plans := NewMemoryPlans(time.Nanosecond)
	id, err := plans.Save(context.Background(), "ada", []codemode.Call{{Name: "todos.delete"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plans.Take(context.Background(), "ada", id); !errors.Is(err, ErrNoPlan) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyFailsForACallerWhoLostAccess(t *testing.T) {
	plans := NewMemoryPlans(0)
	id, _ := plans.Save(context.Background(), "ada", []codemode.Call{{Name: "todos.delete", Input: json.RawMessage(`{}`)}})
	lost, err := codemode.FromSource(codemode.Source{
		Registry: capability.Registry{Capabilities: []capability.Capability{{Name: "todos.delete", RPC: "delete", Roles: []string{"admin"}, NeedsApproval: true}}},
		Execute:  func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil },
	}, capability.Caller{Roles: []string{"member"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPlan(context.Background(), lost, plans, "ada", id); err == nil {
		t.Fatal("applied a plan for a caller who may no longer call it")
	}
}
