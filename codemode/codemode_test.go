package codemode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mukezhz/codemode/capability"
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

func TestFingerprintChangesWhenAContractChanges(t *testing.T) {
	fingerprint := func(readOnly bool) string {
		registry := capability.Registry{Capabilities: []capability.Capability{{Name: "todos.list", RPC: "todo/List", ReadOnly: readOnly}}}
		mode, err := New(registry,
			func(context.Context, string, int) ([]capability.Match, error) { return nil, nil },
			func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil })
		if err != nil {
			t.Fatal(err)
		}
		return mode.(Fingerprinter).Fingerprint()
	}
	if fingerprint(true) == fingerprint(false) {
		t.Fatal("fingerprint ignores the read-only flag")
	}
}
