package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mycompany/code-mode/capability"
)

const (
	createRPC   = "todo.v1.TodoService/Create"
	listRPC     = "todo.v1.TodoService/List"
	completeRPC = "todo.v1.TodoService/Complete"
)

type todo struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
}

type createTodoRequest struct {
	Title string `json:"title"`
}

type createTodoResponse struct {
	Todo todo `json:"todo"`
}

type listTodosResponse struct {
	Todos []todo `json:"todos"`
}

type completeTodoRequest struct {
	ID int64 `json:"id"`
}

type completeTodoResponse struct {
	Todo todo `json:"todo"`
}

type todoApp struct {
	nextID int64
	todos  []todo
}

func (app *todoApp) invoke(rpc string, input []byte) (any, error) {
	switch rpc {
	case createRPC:
		var request createTodoRequest
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		request.Title = strings.TrimSpace(request.Title)
		if request.Title == "" {
			return nil, errors.New("title is required")
		}
		app.nextID++
		item := todo{ID: app.nextID, Title: request.Title}
		app.todos = append(app.todos, item)
		return createTodoResponse{Todo: item}, nil

	case listRPC:
		return listTodosResponse{Todos: append([]todo(nil), app.todos...)}, nil

	case completeRPC:
		var request completeTodoRequest
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		for index := range app.todos {
			if app.todos[index].ID == request.ID {
				app.todos[index].Completed = true
				return completeTodoResponse{Todo: app.todos[index]}, nil
			}
		}
		return nil, fmt.Errorf("todo %d not found", request.ID)

	default:
		return nil, fmt.Errorf("RPC %q is not implemented", rpc)
	}
}

type runtime struct {
	capabilities map[string]capability.Capability
	app          *todoApp
}

func newRuntime(registry capability.Registry) *runtime {
	capabilities := make(map[string]capability.Capability, len(registry.Capabilities))
	for _, item := range registry.Capabilities {
		capabilities[item.Name] = item
	}
	return &runtime{capabilities: capabilities, app: &todoApp{}}
}

func (runtime *runtime) call(name string, input any) (any, error) {
	item, ok := runtime.capabilities[name]
	if !ok {
		return nil, fmt.Errorf("capability %q is not registered", name)
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return runtime.app.invoke(item.RPC, payload)
}

func decode(input []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode input: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("decode input: expected one JSON value")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	data, err := os.ReadFile("registry/registry.json")
	if err != nil {
		return fmt.Errorf("read registry (run from the repository root): %w", err)
	}
	var registry capability.Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return fmt.Errorf("decode registry: %w", err)
	}
	runtime := newRuntime(registry)

	steps := []struct {
		label string
		name  string
		input any
	}{
		{"create", "todos.create", createTodoRequest{Title: "Document code mode"}},
		{"create", "todos.create", createTodoRequest{Title: "Run the Todo demo"}},
		{"list", "todos.list", struct{}{}},
		{"complete", "todos.complete", completeTodoRequest{ID: 1}},
		{"list", "todos.list", struct{}{}},
	}

	for _, step := range steps {
		result, err := runtime.call(step.name, step.input)
		if err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
		output, err := json.Marshal(result)
		if err != nil {
			return err
		}
		fmt.Printf("%-8s %s -> %s\n", step.label, step.name, output)
	}
	return nil
}
