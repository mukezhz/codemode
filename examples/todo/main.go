package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mycompany/code-mode/capability"
	"github.com/mycompany/code-mode/codemode"
	"github.com/mycompany/code-mode/examples/todo/app"
	demosearch "github.com/mycompany/code-mode/examples/todo/search"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	data, err := os.ReadFile("registry/registry.json")
	if err != nil {
		return fmt.Errorf("read registry (run from the repository root): %w", err)
	}
	var registry capability.Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return fmt.Errorf("decode registry: %w", err)
	}
	mode, err := codemode.New(registry, demosearch.New(registry), app.New().Execute)
	if err != nil {
		return err
	}

	matches, err := mode.Search(ctx, "create list and complete todo items", 3)
	if err != nil {
		return err
	}
	fmt.Println("search")
	for _, match := range matches {
		fmt.Printf("  %s -> %s\n", match.Capability.Name, match.Capability.RPC)
	}

	steps := []struct {
		name  string
		input json.RawMessage
	}{
		{"todos.create", json.RawMessage(`{"title":"Document code mode"}`)},
		{"todos.create", json.RawMessage(`{"title":"Run the Todo demo"}`)},
		{"todos.list", json.RawMessage(`{}`)},
		{"todos.complete", json.RawMessage(`{"id":1}`)},
		{"todos.list", json.RawMessage(`{}`)},
	}

	fmt.Println("execute")
	for _, step := range steps {
		output, err := mode.Execute(ctx, step.name, step.input)
		if err != nil {
			return err
		}
		fmt.Printf("  %s(%s) -> %s\n", step.name, step.input, output)
	}
	return nil
}
