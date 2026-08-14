package main

import (
	"testing"

	"github.com/mycompany/code-mode/capability"
)

func TestTodoWorkflow(t *testing.T) {
	runtime := newRuntime(capability.Registry{Capabilities: []capability.Capability{
		{Name: "todos.create", RPC: createRPC},
		{Name: "todos.list", RPC: listRPC},
		{Name: "todos.complete", RPC: completeRPC},
	}})

	created, err := runtime.call("todos.create", map[string]string{"title": "Test Todo flow"})
	if err != nil {
		t.Fatal(err)
	}
	if created.(createTodoResponse).Todo.ID != 1 {
		t.Fatalf("created = %#v", created)
	}
	if _, err := runtime.call("todos.complete", map[string]int64{"id": 1}); err != nil {
		t.Fatal(err)
	}
	listed, err := runtime.call("todos.list", struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	todos := listed.(listTodosResponse).Todos
	if len(todos) != 1 || !todos[0].Completed {
		t.Fatalf("todos = %#v", todos)
	}
}
