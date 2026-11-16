package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/mukezhz/codemode/capability/gosource"
	"github.com/mukezhz/codemode/codemode"
)

type Todo struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
}

type CreateRequest struct {
	Title string `json:"title" desc:"what needs doing"`
}

type TodoResponse struct {
	Todo Todo `json:"todo"`
}

type ListRequest struct{}

type ListResponse struct {
	Todos []Todo `json:"todos"`
}

type CompleteRequest struct {
	ID int64 `json:"id" desc:"the id from todos.list"`
}

type App struct {
	mutex  sync.Mutex
	nextID int64
	todos  []Todo
}

func New() *App {
	return &App{}
}

// Source serves the app from its Go types: no proto, no switch.
func (app *App) Source() (codemode.Source, error) {
	return gosource.New(
		gosource.Func("todos.create", "Creates a todo item with the given title.", gosource.Public(), app.Create),
		gosource.Func("todos.list", "Lists all todo items and their completion state.", gosource.Public(), app.List, gosource.ReadOnly()),
		gosource.Func("todos.complete", "Marks a todo item as completed by ID.", gosource.Public(), app.Complete),
	)
}

func (app *App) Create(_ context.Context, request CreateRequest) (TodoResponse, error) {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return TodoResponse{}, errors.New("title is required")
	}
	app.mutex.Lock()
	defer app.mutex.Unlock()
	app.nextID++
	item := Todo{ID: app.nextID, Title: title}
	app.todos = append(app.todos, item)
	return TodoResponse{Todo: item}, nil
}

func (app *App) List(context.Context, ListRequest) (ListResponse, error) {
	app.mutex.Lock()
	defer app.mutex.Unlock()
	return ListResponse{Todos: append([]Todo{}, app.todos...)}, nil
}

func (app *App) Complete(_ context.Context, request CompleteRequest) (TodoResponse, error) {
	app.mutex.Lock()
	defer app.mutex.Unlock()
	for index := range app.todos {
		if app.todos[index].ID == request.ID {
			app.todos[index].Completed = true
			return TodoResponse{Todo: app.todos[index]}, nil
		}
	}
	return TodoResponse{}, fmt.Errorf("todo %d not found", request.ID)
}

const (
	createRPC   = "todo.v1.TodoService/Create"
	listRPC     = "todo.v1.TodoService/List"
	completeRPC = "todo.v1.TodoService/Complete"
)

// Execute serves the proto registry's RPC paths with the same methods.
// ponytail: hand-written routing until registry.Source dispatches generated services.
func (app *App) Execute(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch rpc {
	case createRPC:
		return serve(ctx, input, app.Create)
	case listRPC:
		return serve(ctx, input, app.List)
	case completeRPC:
		return serve(ctx, input, app.Complete)
	}
	return nil, fmt.Errorf("RPC %q is not implemented", rpc)
}

func serve[In, Out any](ctx context.Context, input json.RawMessage, handler func(context.Context, In) (Out, error)) (json.RawMessage, error) {
	var request In
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, fmt.Errorf("decode input: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("decode input: expected one JSON value")
	}
	response, err := handler(ctx, request)
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}
