package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mukezhz/code-mode/capability"
)

// engine pairs a runner with a program written in its dialect, so every engine
// can be held to the same behaviour with the same assertions.
type engine struct {
	name string
	new  func(Options) (Runner, error)

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
			new:  NewGoja,
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
			new:  NewStarlark,
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
			new:  NewLua,
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
			runner, err := engine.new(Options{Timeout: 5 * time.Second})
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
			runner, err := engine.new(Options{Timeout: 5 * time.Second})
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
			runner, err := engine.new(Options{Timeout: 5 * time.Second, MaxCalls: 3})
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
			runner, err := engine.new(Options{Timeout: 80 * time.Millisecond})
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
			runner, err := engine.new(Options{Timeout: 5 * time.Second})
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
			runner, err := engine.new(Options{})
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
			if _, err := runner.Run(context.Background(), newFake(), exampleOf(engine.name)); err != nil {
				t.Fatalf("the dialect's own example does not run: %v", err)
			}
		})
	}
}

// TestEnginesRenderNeutralTypesInTheirOwnNotation covers every shape the
// registry can emit. The protos in this repository happen to use none of the
// compound ones, so without this the map and bytes paths would ship untried.
func TestEnginesRenderNeutralTypesInTheirOwnNotation(t *testing.T) {
	neutral := map[string]string{
		"flag":   "bool",
		"count":  "number",
		"blob":   "bytes",
		"todo":   "Todo",
		"todos":  "repeated Todo",
		"labels": "map<string, Todo>",
	}

	want := map[string]map[string]string{
		"goja": {
			"flag": "boolean", "count": "number", "blob": "string (base64)",
			"todo": "Todo", "todos": "Todo[]", "labels": "Record<string, Todo>",
		},
		"starlark": {
			"flag": "bool", "count": "int|float", "blob": "string (base64)",
			"todo": "Todo", "todos": "[Todo]", "labels": "{string: Todo}",
		},
		"lua": {
			"flag": "boolean", "count": "number", "blob": "string (base64)",
			"todo": "Todo", "todos": "{Todo}", "labels": "{[string] = Todo}",
		},
	}

	styles := map[string]types{"goja": javascriptTypes, "starlark": starlarkTypes, "lua": luaTypes}
	for engine, expected := range want {
		t.Run(engine, func(t *testing.T) {
			for field, neutralType := range neutral {
				if got := styles[engine].render(neutralType); got != expected[field] {
					t.Errorf("render(%q) = %q, want %q", neutralType, got, expected[field])
				}
			}
		})
	}
}

func exampleOf(name string) string {
	switch name {
	case "goja":
		return javascript.example
	case "starlark":
		return starlarkDialect.example
	default:
		return luaDialect.example
	}
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
			runner, err := New(Options{})
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

// Every engine must satisfy the interface.
var (
	_ Runner = (*gojaRunner)(nil)
	_ Runner = (*starlarkRunner)(nil)
	_ Runner = (*luaRunner)(nil)
)
