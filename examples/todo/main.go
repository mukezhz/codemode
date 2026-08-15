package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mukezhz/code-mode/agent"
	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/codemode"
	coderegistry "github.com/mukezhz/code-mode/registry"

	"github.com/mukezhz/code-mode/examples/todo/app"
	demosearch "github.com/mukezhz/code-mode/examples/todo/search"
	todov1 "github.com/mukezhz/code-mode/gen/todo/v1"
	"github.com/mukezhz/code-mode/llm"
	"github.com/mukezhz/code-mode/sandbox"

	// The demo links every backend so one binary can show all of them. A real
	// application blank-imports only what it runs, which is the point of the
	// split: no cgo unless you asked for SQLite, no goja unless you asked for
	// JavaScript.
	_ "github.com/mukezhz/code-mode/capability/pgstore"
	_ "github.com/mukezhz/code-mode/capability/sqlitestore"
	_ "github.com/mukezhz/code-mode/llm/claude"
	openaiprovider "github.com/mukezhz/code-mode/llm/openai"
	_ "github.com/mukezhz/code-mode/sandbox/goja"
	_ "github.com/mukezhz/code-mode/sandbox/lua"
	_ "github.com/mukezhz/code-mode/sandbox/starlark"
)

func main() {
	request := flag.String("request", "add two todos and finish the first one", "what the agent should do")
	model := flag.String("model", "", "model name, overriding ANTHROPIC_MODEL / OPENAI_MODEL")
	temperature := flag.Float64("temperature", -1, "sampling temperature; negative leaves the provider default")
	instructions := flag.String("instructions", "", "extra guidance appended to the system prompt")
	flag.Parse()

	// A command-line application configures itself from flags and the
	// environment. The library reads neither: it is handed this Options.
	options, err := llm.OptionsFromEnv()
	if errors.Is(err, llm.ErrNoProvider) {
		options = llm.Options{}
	} else if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *model != "" {
		options.Model = *model
	}
	if *temperature >= 0 {
		options.Temperature = llm.Float(*temperature)
	}

	if err := run(context.Background(), *request, options, *instructions); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, request string, options llm.Options, instructions string) error {
	// The registry is derived from the contracts at startup: the generated
	// package carries its own descriptors, so there is no file to build, embed,
	// or keep in step. codemode-registry is there for projects that would
	// rather review registry.json in a pull request.
	registry, err := coderegistry.FromFiles(todov1.File_todo_proto)
	if err != nil {
		return err
	}
	search, closeSearch, err := newSearch(ctx, registry)
	if err != nil {
		return err
	}
	defer closeSearch()

	mode, err := codemode.New(registry, search, app.New().Execute)
	if err != nil {
		return err
	}

	fmt.Printf("request\n  %s\n\n", request)

	model, err := llm.New(options)
	if errors.Is(err, llm.ErrNoProvider) {
		return offline(ctx, mode, request)
	}
	if err != nil {
		return err
	}

	runner, err := sandbox.NewFromEnv(sandbox.Options{})
	if err != nil {
		return err
	}
	defer runner.Close()

	fmt.Printf("model\n  %s\n\nsandbox\n  %s\n\n", model.Name(), runner.Name())
	result, err := agent.Run(ctx, mode, model, request, agent.Options{
		Runner:       runner,
		Instructions: instructions,
	})
	report(result)
	if err != nil {
		return err
	}
	fmt.Printf("answer\n  %s\n", agent.Answer(result.Answer))
	return nil
}

// newSearch returns the ranking function and a cleanup. It prefers the semantic
// index when CAPABILITY_DB_DSN and an embedding endpoint are configured, and
// falls back to the demo's keyword ranker otherwise.
func newSearch(ctx context.Context, registry capability.Registry) (codemode.SearchFunc, func(), error) {
	store, err := capability.OpenStoreFromEnv(ctx)
	if errors.Is(err, capability.ErrNoStore) {
		fmt.Println("search\n  keyword ranker (set CAPABILITY_DB_DSN for semantic search)")
		return demosearch.New(registry), func() {}, nil
	}
	if err != nil {
		return nil, nil, err
	}

	embedder, err := openaiprovider.NewEmbedderFromEnv(openaiprovider.EmbedOptions{})
	if err != nil {
		store.Close()
		return nil, nil, fmt.Errorf("capability store is configured but embeddings are not: %w", err)
	}

	// ponytail: the demo rebuilds on every run because the registry is tiny.
	// Production rebuilds when contracts or the embedding model change.
	if err := capability.Rebuild(ctx, store, registry, capability.Embedder{Model: embedder.Model, Embed: embedder.Embed}); err != nil {
		store.Close()
		return nil, nil, err
	}
	index, err := capability.Open(ctx, store, capability.Embedder{Model: embedder.Model, Embed: embedder.Embed})
	if err != nil {
		store.Close()
		return nil, nil, err
	}

	fmt.Printf("search\n  semantic index (%s)\n", embedder.Model)
	return index.Search, func() { index.Close() }, nil
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
	runner, err := sandbox.NewFromEnv(sandbox.Options{})
	if err != nil {
		return err
	}
	defer runner.Close()

	fmt.Println("model\n  none (set ANTHROPIC_API_KEY or OPENAI_API_KEY to generate the program)")
	fmt.Printf("\nsandbox\n  %s\n", runner.Name())

	matches, err := mode.Search(ctx, request, agent.DefaultLimit)
	if err != nil {
		return err
	}

	fmt.Printf("\nsearch\n")
	for _, match := range matches {
		fmt.Printf("  %s -> %s\n", match.Capability.Name, match.Capability.RPC)
	}
	fmt.Printf("\nprompt\n%s\n", indent(runner.Prompt(matches)))

	program, ok := fixedPrograms[runner.Language()]
	if !ok {
		return fmt.Errorf("no fixed demo program for %s; set an API key to have one written", runner.Language())
	}
	fmt.Printf("\nprogram (fixed, %s)\n%s\n", runner.Language(), indent(program))

	value, err := runner.Run(ctx, mode, program)
	if err != nil {
		return err
	}
	fmt.Printf("\nanswer\n  %s\n", agent.Answer(value))
	return nil
}

// fixedPrograms stand in for model-authored code when no provider is
// configured, one per sandbox dialect. They do the same job three ways: each
// sees only codemode.execute, and each does its looping and filtering inside
// the sandbox, so the host and the model exchange one request and one answer
// rather than one round trip per call.
var fixedPrograms = map[string]string{
	"javascript": `
codemode.execute("todos.create", { title: "Document code mode" });
codemode.execute("todos.create", { title: "Run the Todo demo" });

const { todos } = codemode.execute("todos.list", {});
const pending = todos.filter(todo => !todo.completed);
if (pending.length === 0) {
	return "nothing to finish";
}

const done = codemode.execute("todos.complete", { id: pending[0].id });
return "completed " + done.todo.title + ", " + (pending.length - 1) + " left";
`,

	"starlark": `
codemode.execute("todos.create", {"title": "Document code mode"})
codemode.execute("todos.create", {"title": "Run the Todo demo"})

todos = codemode.execute("todos.list", {})["todos"]
pending = [todo for todo in todos if not todo["completed"]]
if len(pending) == 0:
    return "nothing to finish"

done = codemode.execute("todos.complete", {"id": pending[0]["id"]})
return "completed " + done["todo"]["title"] + ", " + str(len(pending) - 1) + " left"
`,

	"lua": `
codemode.execute("todos.create", { title = "Document code mode" })
codemode.execute("todos.create", { title = "Run the Todo demo" })

local todos = codemode.execute("todos.list", {}).todos
local pending = {}
for _, todo in ipairs(todos) do
  if not todo.completed then
    pending[#pending + 1] = todo
  end
end
if #pending == 0 then
  return "nothing to finish"
end

local done = codemode.execute("todos.complete", { id = pending[1].id })
return "completed " .. done.todo.title .. ", " .. tostring(#pending - 1) .. " left"
`,
}

func indent(text string) string {
	return "  " + strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n  ")
}
