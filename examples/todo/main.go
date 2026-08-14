package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mukezhz/code-mode/agent"
	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/codemode"
	"github.com/mukezhz/code-mode/examples/todo/app"
	demosearch "github.com/mukezhz/code-mode/examples/todo/search"
	"github.com/mukezhz/code-mode/llm"
	"github.com/mukezhz/code-mode/sandbox"
)

func main() {
	request := flag.String("request", "add two todos and finish the first one", "what the agent should do")
	flag.Parse()

	if err := run(context.Background(), *request); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, request string) error {
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

	fmt.Printf("request\n  %s\n\n", request)

	model, err := llm.New()
	if errors.Is(err, llm.ErrNoProvider) {
		return offline(ctx, mode, request)
	}
	if err != nil {
		return err
	}

	fmt.Printf("model\n  %s\n\n", model.Name())
	result, err := agent.Run(ctx, mode, model, request, agent.Options{})
	report(result)
	if err != nil {
		return err
	}
	fmt.Printf("answer\n  %s\n", agent.Answer(result.Answer))
	return nil
}

func report(result agent.Result) {
	if len(result.Matches) > 0 {
		fmt.Println("search")
		for _, match := range result.Matches {
			fmt.Printf("  %s -> %s\n", match.Capability.Name, match.Capability.RPC)
		}
		fmt.Println()
	}
	if result.Source != "" {
		fmt.Printf("program (attempt %d)\n%s\n\n", result.Attempts, indent(result.Source))
	}
}

// offline runs the demo without a model so the flow is inspectable without
// credentials. Everything except the program's authorship is unchanged: search
// still selects the capabilities, and the sandbox still calls the host.
func offline(ctx context.Context, mode codemode.CodeMode, request string) error {
	fmt.Println("model\n  none (set ANTHROPIC_API_KEY or OPENAI_API_KEY to generate the program)")

	matches, err := mode.Search(ctx, request, agent.DefaultLimit)
	if err != nil {
		return err
	}
	fmt.Printf("\nsearch\n")
	for _, match := range matches {
		fmt.Printf("  %s -> %s\n", match.Capability.Name, match.Capability.RPC)
	}
	fmt.Printf("\nprompt\n%s\n", indent(agent.SystemPrompt(matches)))
	fmt.Printf("\nprogram (fixed)\n%s\n", indent(fixedProgram))

	value, err := sandbox.Run(ctx, mode, fixedProgram, sandbox.Options{Timeout: agent.DefaultTimeout})
	if err != nil {
		return err
	}
	fmt.Printf("\nanswer\n  %s\n", agent.Answer(value))
	return nil
}

// fixedProgram stands in for model-authored code when no provider is
// configured. It sees only codemode.execute; the loop and the filtering run
// inside the sandbox, so the host and the model exchange one request and one
// answer rather than one round trip per call.
const fixedProgram = `
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
