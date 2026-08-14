package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	createRPC   = "todo.v1.TodoService/Create"
	listRPC     = "todo.v1.TodoService/List"
	completeRPC = "todo.v1.TodoService/Complete"
)

type Todo struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
}

type createRequest struct {
	Title string `json:"title"`
}

type createResponse struct {
	Todo Todo `json:"todo"`
}

type listResponse struct {
	Todos []Todo `json:"todos"`
}

type completeRequest struct {
	ID int64 `json:"id"`
}

type completeResponse struct {
	Todo Todo `json:"todo"`
}

type App struct {
	nextID int64
	todos  []Todo
}

func New() *App {
	return &App{}
}

func (app *App) Execute(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var output any
	switch rpc {
	case createRPC:
		var request createRequest
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		request.Title = strings.TrimSpace(request.Title)
		if request.Title == "" {
			return nil, errors.New("title is required")
		}
		app.nextID++
		item := Todo{ID: app.nextID, Title: request.Title}
		app.todos = append(app.todos, item)
		output = createResponse{Todo: item}

	case listRPC:
		var request struct{}
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		output = listResponse{Todos: append([]Todo(nil), app.todos...)}

	case completeRPC:
		var request completeRequest
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		for index := range app.todos {
			if app.todos[index].ID == request.ID {
				app.todos[index].Completed = true
				output = completeResponse{Todo: app.todos[index]}
				break
			}
		}
		if output == nil {
			return nil, fmt.Errorf("todo %d not found", request.ID)
		}

	default:
		return nil, fmt.Errorf("RPC %q is not implemented", rpc)
	}
	return json.Marshal(output)
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
