package codemode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mycompany/code-mode/capability"
)

func TestSearchAndExecute(t *testing.T) {
	registry := capability.Registry{Capabilities: []capability.Capability{{
		Name: "todos.create",
		RPC:  "todo.v1.TodoService/Create",
	}}}
	searchCalled := false
	mode, err := New(registry,
		func(_ context.Context, query string, limit int) ([]capability.Match, error) {
			searchCalled = query == "add a task" && limit == 1
			return []capability.Match{{Capability: registry.Capabilities[0]}}, nil
		},
		func(_ context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
			if rpc != "todo.v1.TodoService/Create" || string(input) != `{"title":"Test"}` {
				t.Fatalf("execute(%q, %s)", rpc, input)
			}
			return json.RawMessage(`{"todo":{"id":1}}`), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := mode.Search(context.Background(), "add a task", 1); err != nil {
		t.Fatal(err)
	}
	if !searchCalled {
		t.Fatal("search was not delegated")
	}
	output, err := mode.Execute(context.Background(), "todos.create", json.RawMessage(`{"title":"Test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != `{"todo":{"id":1}}` {
		t.Fatalf("output = %s", output)
	}
}
