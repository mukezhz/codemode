package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/codemode/sourcetest"
	coderegistry "github.com/mukezhz/codemode/registry"
	"github.com/mukezhz/codemode/sandbox"
	_ "github.com/mukezhz/codemode/sandbox/goja"

	todov1 "github.com/mukezhz/codemode/examples/todo/gen/todo/v1"
)

func TestWorkflow(t *testing.T) {
	app := New()
	if _, err := app.Execute(context.Background(), createRPC, json.RawMessage(`{"title":"Test Todo flow"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Execute(context.Background(), completeRPC, json.RawMessage(`{"id":1}`)); err != nil {
		t.Fatal(err)
	}
	output, err := app.Execute(context.Background(), listRPC, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var response ListResponse
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Todos) != 1 || !response.Todos[0].Completed {
		t.Fatalf("todos = %#v", response.Todos)
	}
}

func TestEmptyListIsAnArray(t *testing.T) {
	output, err := New().Execute(context.Background(), listRPC, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != `{"todos":[]}` {
		t.Fatalf("output = %s, want an empty array so .length works", output)
	}
}

func TestGoSourcePassesTheContract(t *testing.T) {
	source, err := New().Source()
	if err != nil {
		t.Fatal(err)
	}
	sourcetest.Run(t, source)
}

const program = `
codemode.execute("todos.create", { title: "Document code mode" });
codemode.execute("todos.create", { title: "Run the Todo demo" });
const { todos } = codemode.execute("todos.list", {});
const pending = todos.filter(todo => !todo.completed);
const done = codemode.execute("todos.complete", { id: pending[0].id });
return "completed " + done.todo.title + ", " + (pending.length - 1) + " left";`

func TestGoAndProtoSourcesAnswerAlike(t *testing.T) {
	runner, err := sandbox.New(sandbox.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	fromGo, err := New().Source()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := coderegistry.FromFiles(todov1.File_todo_proto)
	if err != nil {
		t.Fatal(err)
	}
	fromProto := codemode.Source{Registry: registry, Execute: New().Execute}

	for name, source := range map[string]codemode.Source{"go": fromGo, "proto": fromProto} {
		mode, err := codemode.FromSource(source, capability.Caller{})
		if err != nil {
			t.Fatal(err)
		}
		value, err := runner.Run(context.Background(), mode, program)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if value != "completed Document code mode, 1 left" {
			t.Fatalf("%s: value = %v", name, value)
		}
	}
}
