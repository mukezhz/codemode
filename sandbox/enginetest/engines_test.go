package enginetest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/sandbox"
	"github.com/mukezhz/code-mode/sandbox/goja"
	"github.com/mukezhz/code-mode/sandbox/lua"
	"github.com/mukezhz/code-mode/sandbox/starlark"
)

// engine pairs a runner with a program written in its dialect, so every engine
// can be held to the same behaviour with the same assertions.
type engine struct {
	name string
	new  func(sandbox.Options) (sandbox.Runner, error)

	// compose reads a list, filters it, calls again, and returns a string.
	compose string
	// unregistered calls a capability that is not in the registry.
	unregistered string
	// budget calls a capability far more often than the budget allows.
	budget string
	// forever never terminates on its own.
	forever string
	// nothing returns no value.
	nothing string
}

func engines() []engine {
	return []engine{
		{
			name: "goja",
			new:  goja.New,
			compose: `const { todos } = codemode.execute("todos.list", {});
const pending = todos.filter(t => !t.completed);
codemode.execute("todos.complete", { id: pending[0].id });
return "done: " + pending[0].title;`,
			unregistered: `return codemode.execute("todos.purge", {});`,
			budget: `for (let i = 0; i < 100; i++) { codemode.execute("todos.list", {}); }
return "unreachable";`,
			forever: `while (true) {}`,
			nothing: `const x = 1;`,
		},
		{
			name: "starlark",
			new:  starlark.New,
			compose: `todos = codemode.execute("todos.list", {})["todos"]
pending = [t for t in todos if not t["completed"]]
codemode.execute("todos.complete", {"id": pending[0]["id"]})
return "done: " + pending[0]["title"]`,
			unregistered: `return codemode.execute("todos.purge", {})`,
			budget: `for i in range(100):
    codemode.execute("todos.list", {})
return "unreachable"`,
			// Starlark has no while loop; a long range is the runaway shape it
			// does allow.
			forever: `total = 0
for i in range(100000000):
    total = total + i
return total`,
			nothing: `x = 1`,
		},
		{
			name: "lua",
			new:  lua.New,
			compose: `local todos = codemode.execute("todos.list", {}).todos
local pending = {}
for _, t in ipairs(todos) do
  if not t.completed then pending[#pending + 1] = t end
end
codemode.execute("todos.complete", { id = pending[1].id })
return "done: " .. pending[1].title`,
			unregistered: `return codemode.execute("todos.purge", {})`,
			budget: `for i = 1, 100 do codemode.execute("todos.list", {}) end
return "unreachable"`,
			forever: `while true do end`,
			nothing: `local x = 1`,
		},
	}
}

func TestEnginesComposeCapabilityCalls(t *testing.T) {
	for _, engine := range engines() {
		t.Run(engine.name, func(t *testing.T) {
			mode := newFake()
			runner, err := engine.new(sandbox.Options{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()

			value, err := runner.Run(context.Background(), mode, engine.compose)
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			if value != "done: write docs" {
				t.Fatalf("value = %#v, want %q", value, "done: write docs")
			}

			want := []string{`todos.list({})`, `todos.complete({"id":1})`}
			if len(mode.calls) != len(want) {
				t.Fatalf("calls = %v, want %v", mode.calls, want)
			}
			for i, call := range want {
				if mode.calls[i] != call {
					t.Fatalf("call %d = %q, want %q", i, mode.calls[i], call)
				}
			}
		})
	}
}

func TestEnginesRejectUnregisteredCapabilities(t *testing.T) {
	for _, engine := range engines() {
		t.Run(engine.name, func(t *testing.T) {
			runner, err := engine.new(sandbox.Options{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()

			_, err = runner.Run(context.Background(), newFake(), engine.unregistered)
			if err == nil {
				t.Fatal("Run returned no error for an unregistered capability")
			}
			if !strings.Contains(err.Error(), "todos.purge") {
				t.Fatalf("error = %v, want it to name the capability", err)
			}
		})
	}
}

func TestEnginesEnforceTheCallBudget(t *testing.T) {
	for _, engine := range engines() {
		t.Run(engine.name, func(t *testing.T) {
			mode := newFake()
			runner, err := engine.new(sandbox.Options{Timeout: 5 * time.Second, MaxCalls: 3})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()

			if _, err := runner.Run(context.Background(), mode, engine.budget); err == nil {
				t.Fatal("Run returned no error for an exhausted budget")
			}
			if len(mode.calls) != 3 {
				t.Fatalf("calls = %d, want 3", len(mode.calls))
			}
		})
	}
}

func TestEnginesStopRunawayPrograms(t *testing.T) {
	for _, engine := range engines() {
		t.Run(engine.name, func(t *testing.T) {
			runner, err := engine.new(sandbox.Options{Timeout: 80 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()

			started := time.Now()
			if _, err := runner.Run(context.Background(), newFake(), engine.forever); err == nil {
				t.Fatal("Run returned no error for a program that never terminates")
			}
			if elapsed := time.Since(started); elapsed > 3*time.Second {
				t.Fatalf("program ran for %s after an 80ms timeout", elapsed)
			}
		})
	}
}

func TestEnginesReturnNilWhenTheProgramReturnsNothing(t *testing.T) {
	for _, engine := range engines() {
		t.Run(engine.name, func(t *testing.T) {
			runner, err := engine.new(sandbox.Options{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()

			value, err := runner.Run(context.Background(), newFake(), engine.nothing)
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			if value != nil {
				t.Fatalf("value = %#v, want nil", value)
			}
		})
	}
}

func TestEnginesRenderTheirOwnDialect(t *testing.T) {
	matches := []capability.Match{{Capability: capability.Capability{
		Name:        "todos.complete",
		Description: "Mark a todo as complete",
		Input:       capability.TypeSchema{Fields: map[string]string{"id": "number"}},
		Output:      capability.TypeSchema{Fields: map[string]string{"todo": "Todo"}},
	}}}

	// Each engine must name the capability, describe it, and do so in syntax
	// its own interpreter would accept.
	want := map[string][]string{
		"goja":     {"todos.complete", "Mark a todo as complete", "/**", "id: number"},
		"starlark": {"todos.complete", "Mark a todo as complete", "#", `"id": int|float`},
		"lua":      {"todos.complete", "Mark a todo as complete", "--", "id = number"},
	}

	for _, engine := range engines() {
		t.Run(engine.name, func(t *testing.T) {
			runner, err := engine.new(sandbox.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()

			prompt := runner.Prompt(matches)
			for _, fragment := range want[engine.name] {
				if !strings.Contains(prompt, fragment) {
					t.Fatalf("prompt is missing %q:\n%s", fragment, prompt)
				}
			}
			// The example in the prompt must be written in the same dialect the
			// engine evaluates, or it teaches the model the wrong syntax.
			if _, err := runner.Run(context.Background(), newFake(), exampleOf(t, runner)); err != nil {
				t.Fatalf("the dialect's own example does not run: %v", err)
			}
		})
	}
}

// TestEnginesRenderNeutralTypesInTheirOwnNotation covers every shape the
// registry can emit. The protos in this repository happen to use none of the
// compound ones, so without this the map and bytes paths would ship untried.
// It reads the rendered prompt rather than the dialect's type table, so it
// tests the path a model actually sees.
func TestEnginesRenderNeutralTypesInTheirOwnNotation(t *testing.T) {
	matches := []capability.Match{{Capability: capability.Capability{
		Name:        "shapes.all",
		Description: "Every neutral type in one signature",
		Input:       capability.TypeSchema{Fields: map[string]string{"flag": "bool", "count": "number"}},
		Output: capability.TypeSchema{Fields: map[string]string{
			"blob": "bytes", "todo": "Todo", "todos": "repeated Todo", "labels": "map<string, Todo>",
		}},
	}}}

	want := map[string][]string{
		"goja":     {"boolean", "number", "string (base64)", "Todo[]", "Record<string, Todo>"},
		"starlark": {"bool", "int|float", "string (base64)", "[Todo]", "{string: Todo}"},
		"lua":      {"boolean", "number", "string (base64)", "{Todo}", "{[string] = Todo}"},
	}

	for _, engine := range engines() {
		t.Run(engine.name, func(t *testing.T) {
			runner, err := engine.new(sandbox.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()

			prompt := runner.Prompt(matches)
			for _, fragment := range want[engine.name] {
				if !strings.Contains(prompt, fragment) {
					t.Errorf("prompt is missing %q:\n%s", fragment, prompt)
				}
			}
		})
	}
}

// exampleOf pulls the worked example out of a rendered prompt. Taking it from
// the prompt rather than the engine's own variable is the point: what gets
// tested is the text a model is actually shown.
func exampleOf(t *testing.T, runner sandbox.Runner) string {
	t.Helper()
	const marker = "Example of the shape expected:\n\n"
	prompt := runner.Prompt(nil)
	_, example, found := strings.Cut(prompt, marker)
	if !found {
		t.Fatalf("prompt has no example section:\n%s", prompt)
	}
	return example
}

func TestNewBuildsEveryEngine(t *testing.T) {
	for alias, language := range map[string]string{
		"goja":       "javascript",
		"js":         "javascript",
		"javascript": "javascript",
		"starlark":   "starlark",
		"python":     "starlark",
		"lua":        "lua",
		"":           "javascript", // the default
	} {
		t.Run("alias="+alias, func(t *testing.T) {
			t.Setenv("SANDBOX_ENGINE", alias)
			runner, err := sandbox.New(sandbox.Options{})
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			defer runner.Close()
			if runner.Language() != language {
				t.Fatalf("Language() = %q, want %q", runner.Language(), language)
			}
		})
	}
}

// Every engine constructor must return the interface callers depend on.
var (
	_ func(sandbox.Options) (sandbox.Runner, error) = goja.New
	_ func(sandbox.Options) (sandbox.Runner, error) = starlark.New
	_ func(sandbox.Options) (sandbox.Runner, error) = lua.New
)
