package app

import (
	"context"
	"encoding/json"
	"testing"
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
	var response listResponse
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Todos) != 1 || !response.Todos[0].Completed {
		t.Fatalf("todos = %#v", response.Todos)
	}
}
