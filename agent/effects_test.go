package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
)

// effects is a real runtime with one read, one write, and one denied capability.
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
			return []capability.Match{{Capability: registry.Capabilities[0]}}, nil
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

func TestPlanRecordsWritesAndApplyRunsThem(t *testing.T) {
	mode, executed := effects(t)
	author := &model{responses: []string{
		`const { todos } = codemode.execute("todos.list", {});
		 codemode.execute("todos.complete", {id: todos[0].id});
		 return "planned";`,
	}}

	result, err := Run(context.Background(), mode, author, "finish all", Options{Runner: newRunner(t), Plan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(*executed) != 1 || (*executed)[0] != "todo/List" {
		t.Fatalf("executed = %v, want only the read", *executed)
	}
	if len(result.Planned) != 1 || string(result.Planned[0].Input) != `{"id":1}` {
		t.Fatalf("planned = %+v", result.Planned)
	}

	calls, err := Apply(context.Background(), mode, result.Planned)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || len(*executed) != 2 || (*executed)[1] != "todo/Complete" {
		t.Fatalf("calls = %+v, executed = %v", calls, *executed)
	}
}
