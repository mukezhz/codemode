package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"github.com/mukezhz/code-mode
github.com/mukezhz/code-mode
	"github.com/mukezhz/code-mode/capability"
	"github.com/github.com/mukezhz/code-mode
	"github.com/mycompany/code-mode/examples/todo/app"
	demosearch "github.com/mycompany/code-mode/examples/todo/search"
	"github.com/mycompany/code-mode/sandbox"
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

	const request = "add two todos and finish the first one"

	matches, err := mode.Search(ctx, request, 3)
	if err != nil {
		return err
	}
	fmt.Println("search")
	for _, match := range matches {
		fmt.Printf("  %s -> %s\n", match.Capability.Name, match.Capability.RPC)
	}

	// A production host sends these declarations and the request to a model and
	// receives a program back. The demo uses a fixed program so the flow runs
	// without a model, but nothing else about the path changes.
	fmt.Printf("\nprompt\n%s\n", indent(sandbox.Declarations(matches)))

	value, err := sandbox.Run(ctx, mode, program, sandbox.Options{Timeout: 5 * time.Second})
	if err != nil {
		return err
	}
	fmt.Printf("answer\n  %v\n", value)
	return nil
}

// program stands in for model-authored code. It sees only codemode.execute;
// the loop and the filtering run inside the sandbox, so the host and the model
// exchange one request and one answer rather than one round trip per call.
const program = `
	codemode.execute("todos.create", { title: "Document code mode" });
	codemode.execute("todos.create", { title: "Run the Todo demo" });

	const { todos } = codemode.execute("todos.list", {});
	const pending = todos.filter(todo => !todo.completed);
	if (pending.length === 0) {
		return "nothing to finish";
	}

	const done = codemode.execute("todos.complete", { id: pending[0].id });
	return "completed " + done.todo.title + ", " + (pending.length - 1) + " left";
`

func indent(text string) string {
	return "  " + strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n  ")
}
