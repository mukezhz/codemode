package gosource_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/capability/gosource"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/codemode/sourcetest"
	"github.com/mukezhz/codemode/sandbox"
	_ "github.com/mukezhz/codemode/sandbox/goja"
)

type Todo struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

type ListRequest struct {
	Done *bool `json:"done" desc:"only todos in this state; leave out for all"`
}

type ListResponse struct {
	Todos []Todo `json:"todos"`
}

type DeleteRequest struct {
	ID int `json:"id"`
}

type app struct {
	todos   []Todo
	deleted []int
}

func (app *app) list(_ context.Context, request ListRequest) (ListResponse, error) {
	var todos []Todo
	for _, todo := range app.todos {
		if request.Done == nil || todo.Done == *request.Done {
			todos = append(todos, todo)
		}
	}
	return ListResponse{Todos: todos}, nil
}

func (app *app) remove(_ context.Context, request DeleteRequest) (struct{}, error) {
	app.deleted = append(app.deleted, request.ID)
	return struct{}{}, nil
}

func todos(t *testing.T) (codemode.Source, *app) {
	t.Helper()
	state := &app{todos: []Todo{{ID: 1, Title: "write docs", Done: true}, {ID: 2, Title: "ship"}}}
	source, err := gosource.New(
		gosource.Func("todos.list", "Lists todos", gosource.Public(), state.list, gosource.ReadOnly()),
		gosource.Func("todos.delete", "Deletes a todo", gosource.Roles("member"), state.remove, gosource.NeedsApproval()),
	)
	if err != nil {
		t.Fatal(err)
	}
	return source, state
}

func TestSourcePassesTheContract(t *testing.T) {
	source, _ := todos(t)
	sourcetest.Run(t, source)
}

func TestSchemaComesFromTheStructs(t *testing.T) {
	source, _ := todos(t)
	list := source.Registry.Capabilities[0]
	if list.Input.Fields["done"] != "bool" || list.Output.Fields["todos"] != "repeated Todo" || list.Types["Todo"].Fields["id"] != "integer" {
		t.Fatalf("list = %+v", list)
	}
	if list.Input.Descriptions["done"] == "" || !list.ReadOnly || !list.Unrestricted() {
		t.Fatalf("list = %+v", list)
	}
	remove := source.Registry.Capabilities[1]
	if strings.Join(remove.Roles, ",") != "member" || !remove.NeedsApproval || remove.ReadOnly || remove.RPC != "todos.delete" {
		t.Fatalf("delete = %+v", remove)
	}
}

func TestAProgramCallsTypedFunctions(t *testing.T) {
	source, state := todos(t)
	mode, err := codemode.FromSource(source, capability.Caller{Roles: []string{"member"}})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := sandbox.New(sandbox.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	value, err := runner.Run(context.Background(), mode, `
		const { todos } = codemode.execute("todos.list", { done: false });
		return todos.map(todo => todo.title).join(",");`)
	if err != nil || value != "ship" {
		t.Fatalf("value = %v, err = %v", value, err)
	}

	_, err = runner.Run(context.Background(), mode, `return codemode.execute("todos.list", { done: "no" });`)
	if err == nil || !strings.Contains(err.Error(), `done: want bool, got "no"`) {
		t.Fatalf("err = %v", err)
	}

	_, err = runner.Run(context.Background(), mode, `codemode.execute("todos.delete", { id: 1 }); return "ok";`)
	if !errors.Is(err, codemode.ErrPermanent) || len(state.deleted) != 0 {
		t.Fatalf("a delete that needs approval ran without one: err = %v, deleted = %v", err, state.deleted)
	}
}

func TestAnEmptyResultIsAListNotNull(t *testing.T) {
	source, _ := todos(t)
	output, err := source.Execute(context.Background(), "todos.list", json.RawMessage(`{"done":false,"x":1}`))
	if err == nil {
		t.Fatalf("an unknown field reached the handler: %s", output)
	}
	empty, err := gosource.New(gosource.Func("todos.none", "Lists nothing", gosource.Public(),
		func(context.Context, struct{}) (ListResponse, error) { return ListResponse{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	output, err = empty.Execute(context.Background(), "todos.none", nil)
	if err != nil || string(output) != `{"todos":[]}` {
		t.Fatalf("output = %s, err = %v", output, err)
	}
}

func TestAHandlerPanicIsAnError(t *testing.T) {
	source, err := gosource.New(gosource.Func("todos.boom", "Panics", gosource.Public(),
		func(context.Context, struct{}) (struct{}, error) { panic("boom") }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Execute(context.Background(), "todos.boom", nil); err == nil || !strings.Contains(err.Error(), "panicked: boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRefusesUnsafeOrBrokenDefinitions(t *testing.T) {
	noop := func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }
	type other struct {
		Todo string `json:"todo"`
	}
	for name, definition := range map[string]gosource.Definition{
		"no access":          gosource.Func("todos.a", "A", nil, noop),
		"empty roles":        gosource.Func("todos.a", "A", gosource.Roles(" "), noop),
		"bad permission":     gosource.Func("todos.a", "A", gosource.Permissions("todos"), noop),
		"bad name":           gosource.Func("Todos", "A", gosource.Public(), noop),
		"no description":     gosource.Func("todos.a", " ", gosource.Public(), noop),
		"read-only approval": gosource.Func("todos.a", "A", gosource.Public(), noop, gosource.ReadOnly(), gosource.NeedsApproval()),
		"not a struct": gosource.Func("todos.a", "A", gosource.Public(),
			func(context.Context, string) (struct{}, error) { return struct{}{}, nil }),
	} {
		if _, err := gosource.New(definition); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := gosource.New(gosource.Func("todos.a", "A", gosource.Public(), noop), gosource.Func("todos.a", "A", gosource.Public(), noop)); err == nil {
		t.Error("a name defined twice was accepted")
	}
}
