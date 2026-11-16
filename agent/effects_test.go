package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/program"
)

func effects(t *testing.T) (codemode.CodeMode, *[]string) {
	t.Helper()
	registry := capability.Registry{Capabilities: []capability.Capability{
		{Name: "todos.list", RPC: "todo/List", ReadOnly: true},
		{Name: "todos.complete", RPC: "todo/Complete"},
		{Name: "todos.secret", RPC: "todo/Secret", ReadOnly: true},
	}}
	var executed []string
	mode, err := codemode.New(registry,
		func(context.Context, string, int) ([]capability.Match, error) {
			matches := make([]capability.Match, len(registry.Capabilities))
			for index, item := range registry.Capabilities {
				matches[index] = capability.Match{Capability: item}
			}
			return matches, nil
		},
		func(_ context.Context, rpc string, _ json.RawMessage) (json.RawMessage, error) {
			executed = append(executed, rpc)
			if rpc == "todo/Secret" {
				return nil, codemode.Permanent(errors.New("permission denied"))
			}
			return json.RawMessage(`{"todos":[{"id":1}]}`), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return mode, &executed
}

func TestRunDoesNotRetryAProgramThatFailedAfterAWrite(t *testing.T) {
	mode, executed := effects(t)
	author := &model{responses: []string{
		`codemode.execute("todos.complete", {id: 1}); throw new Error("typo");`,
		`return "second attempt";`,
	}}

	result, err := Run(context.Background(), mode, author, "finish todo", Options{Runner: newRunner(t)})
	if !errors.Is(err, ErrNotRetried) {
		t.Fatalf("err = %v, want ErrNotRetried", err)
	}
	if len(*executed) != 1 || result.Attempts != 1 {
		t.Fatalf("executed = %v, attempts = %d; the write must happen once", *executed, result.Attempts)
	}
	if len(result.Calls) != 1 || result.Calls[0].Name != "todos.complete" {
		t.Fatalf("calls = %+v", result.Calls)
	}
}

func TestRunRetriesAProgramThatOnlyRead(t *testing.T) {
	mode, _ := effects(t)
	author := &model{responses: []string{
		`codemode.execute("todos.list", {}); throw new Error("typo");`,
		`return "fixed";`,
	}}

	result, err := Run(context.Background(), mode, author, "list", Options{Runner: newRunner(t)})
	if err != nil || result.Answer != "fixed" || result.Attempts != 2 {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}

func TestRunStopsOnAPermanentError(t *testing.T) {
	mode, _ := effects(t)
	author := &model{responses: []string{`return codemode.execute("todos.secret", {});`, `return "workaround";`}}

	result, err := Run(context.Background(), mode, author, "read secret", Options{Runner: newRunner(t)})
	if !errors.Is(err, codemode.ErrPermanent) || result.Attempts != 1 {
		t.Fatalf("err = %v, attempts = %d", err, result.Attempts)
	}
}

func TestHoldWritesIsADryRunThatApprovalApplies(t *testing.T) {
	mode, executed := effects(t)
	author := &model{responses: []string{
		`const { todos } = codemode.execute("todos.list", {});
		 codemode.execute("todos.complete", {id: todos[0].id});
		 return "held";`,
	}}
	plans := NewPlans(NewMemoryPlanStore(), PlanOptions{})

	_, err := Run(context.Background(), mode, author, "finish all", Options{Runner: newRunner(t), HoldWrites: true, Plans: plans, PlanOwner: "ada"})
	var pending *Pending
	if !errors.As(err, &pending) {
		t.Fatalf("err = %v, want *Pending", err)
	}
	if len(*executed) != 1 || (*executed)[0] != "todo/List" {
		t.Fatalf("executed = %v, want only the read", *executed)
	}
	if len(pending.Calls) != 1 || string(pending.Calls[0].Input) != `{"id":1}` {
		t.Fatalf("held = %+v", pending.Calls)
	}

	plan, err := plans.Approve(context.Background(), mode, "ada", pending.ID)
	if err != nil || len(plan.Applied) != 1 || len(*executed) != 2 || (*executed)[1] != "todo/Complete" {
		t.Fatalf("plan = %+v, executed = %v, err = %v", plan, *executed, err)
	}
}

type metered struct{ model }

func (metered *metered) Complete(ctx context.Context, system, user string) (string, error) {
	llm.Report(ctx, llm.Usage{Input: 60, Output: 40})
	return metered.model.Complete(ctx, system, user)
}

func TestRunStopsRepairingWhenTheTokenBudgetIsSpent(t *testing.T) {
	mode, _ := effects(t)
	author := &metered{model{responses: []string{`throw new Error("a")`, `throw new Error("b")`, `return "c";`}}}

	result, err := Run(context.Background(), mode, author, "list", Options{Runner: newRunner(t), MaxTokens: 150})
	if !errors.Is(err, ErrTokenBudget) || result.Attempts != 2 {
		t.Fatalf("err = %v, attempts = %d; want the budget to stop the third attempt", err, result.Attempts)
	}
	if result.Usage.Total() != 200 {
		t.Fatalf("usage = %+v, want 200 tokens", result.Usage)
	}
}

func TestOnEventSeesEachCallWithItsInput(t *testing.T) {
	mode, _ := effects(t)
	author := &model{responses: []string{`codemode.execute("todos.list", {}); return codemode.execute("todos.list", {}).todos.length;`}}
	var seen []string

	_, err := Run(context.Background(), mode, author, "list", Options{Runner: newRunner(t), OnEvent: func(event Event) {
		if event.Kind == EventCallDone {
			seen = append(seen, event.Name+string(event.Input))
		}
	}})
	if err != nil || len(seen) != 2 || seen[0] != "todos.list{}" {
		t.Fatalf("seen = %v, err = %v", seen, err)
	}
}

func TestRunNeverCachesAProgramThatCanWrite(t *testing.T) {
	for name, source := range map[string]string{
		"wrote":   `codemode.execute("todos.complete", {id: 1}); return "done";`,
		"held":    `codemode.execute("todos.complete", {id: 1}); return "held";`,
		"skipped": `const { todos } = codemode.execute("todos.list", {}); if (todos.length === 0) { codemode.execute("todos.complete", {id: 1}); } return "ok";`,
	} {
		t.Run(name, func(t *testing.T) {
			mode, _ := effects(t)
			author := &model{responses: []string{source, source}}
			options := Options{Runner: newRunner(t), Cache: program.NewExactCache(), HoldWrites: name == "held"}
			for run := 1; run <= 2; run++ {
				result, err := Run(context.Background(), mode, author, "finish todo 1", options)
				var pending *Pending
				if err != nil && !errors.As(err, &pending) {
					t.Fatalf("run %d: %v", run, err)
				}
				if result.Cached {
					t.Fatalf("run %d reused a program that can write", run)
				}
			}
		})
	}
}

func TestRunCachesAProgramThatOnlyReads(t *testing.T) {
	mode, _ := effects(t)
	author := &model{responses: []string{`return codemode.execute("todos.list", {}).todos.length;`}}
	options := Options{Runner: newRunner(t), Cache: program.NewExactCache()}
	if _, err := Run(context.Background(), mode, author, "count todos", options); err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), mode, author, "count todos", options)
	if err != nil || !result.Cached {
		t.Fatalf("result = %+v, err = %v; a read-only program should be reused", result, err)
	}
}
