package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	coderegistry "github.com/mukezhz/codemode/registry"

	"github.com/mukezhz/codemode/examples/todo/app"
	todov1 "github.com/mukezhz/codemode/examples/todo/gen/todo/v1"
	demosearch "github.com/mukezhz/codemode/examples/todo/search"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/sandbox"

	// The demo links every backend; a real app blank-imports only what it runs.
	_ "github.com/mukezhz/codemode/capability/pgstore"
	_ "github.com/mukezhz/codemode/capability/sqlitestore"
	_ "github.com/mukezhz/codemode/llm/claude"
	openaiprovider "github.com/mukezhz/codemode/llm/openai"
	_ "github.com/mukezhz/codemode/sandbox/goja"
	_ "github.com/mukezhz/codemode/sandbox/lua"
	_ "github.com/mukezhz/codemode/sandbox/starlark"
	_ "github.com/mukezhz/codemode/sandbox/subprocess"
)

func main() {
	request := flag.String("request", "add two todos and finish the first one", "what the agent should do")
	model := flag.String("model", "", "model name, overriding ANTHROPIC_MODEL / OPENAI_MODEL")
	temperature := flag.Float64("temperature", -1, "sampling temperature; negative leaves the provider default")
	instructions := flag.String("instructions", "", "extra guidance appended to the system prompt")
	flag.Parse()

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

	// ponytail: rebuilt every run because the registry is tiny.
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
		origin := fmt.Sprintf("attempt %d", result.Attempts)
		if result.Cached {
			origin = "cached"
		}
		fmt.Printf("program (%s)\n%s\n\n", origin, indent(result.Source))
	}
	fmt.Printf("timing\n  %s\n\n", result.Timing)
}

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
