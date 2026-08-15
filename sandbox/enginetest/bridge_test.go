package enginetest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/codemode"
	"github.com/mukezhz/code-mode/sandbox"

	// Every engine this module ships, so sandbox.New can select any of them.
	// A real binary imports only the ones it runs.
	_ "github.com/mukezhz/code-mode/sandbox/goja"
	_ "github.com/mukezhz/code-mode/sandbox/lua"
	_ "github.com/mukezhz/code-mode/sandbox/starlark"
)

// fake records the capability calls a program makes and answers them from a
// fixed script, so tests exercise the bridge rather than a real application.
type fake struct {
	responses map[string]string
	calls     []string
}

func (fake *fake) Search(context.Context, string, int) ([]capability.Match, error) {
	return nil, errors.New("search is not exposed to the sandbox")
}

func (fake *fake) Execute(_ context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	fake.calls = append(fake.calls, name+"("+string(input)+")")
	response, ok := fake.responses[name]
	if !ok {
		return nil, errors.New("capability \"" + name + "\" is not registered")
	}
	return json.RawMessage(response), nil
}

func newFake() *fake {
	return &fake{responses: map[string]string{
		"todos.list":     `{"todos":[{"id":1,"title":"write docs","completed":false}]}`,
		"todos.complete": `{"todo":{"id":1,"title":"write docs","completed":true}}`,
	}}
}

const options = time.Second

func TestNewSelectsTheEngineFromTheEnvironment(t *testing.T) {
	t.Setenv("SANDBOX_ENGINE", "")
	runner, err := sandbox.New(sandbox.Options{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer runner.Close()
	if !strings.Contains(runner.Name(), "goja") {
		t.Fatalf("Name() = %q, want it to name the goja engine", runner.Name())
	}
}

func TestNewRejectsAnUnknownEngine(t *testing.T) {
	t.Setenv("SANDBOX_ENGINE", "wasm")
	if _, err := sandbox.New(sandbox.Options{}); err == nil {
		t.Fatal("New returned no error for an unknown engine")
	}
}

func TestNewReadsBoundsFromTheEnvironment(t *testing.T) {
	t.Setenv("SANDBOX_ENGINE", "")
	t.Setenv("SANDBOX_TIMEOUT", "7s")
	t.Setenv("SANDBOX_MAX_CALLS", "9")

	runner, err := sandbox.New(sandbox.Options{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer runner.Close()
	for _, want := range []string{"7s", "9"} {
		if !strings.Contains(runner.Name(), want) {
			t.Fatalf("Name() = %q, want it to report %q", runner.Name(), want)
		}
	}

	// An explicitly set field wins over the environment.
	explicit, err := sandbox.New(sandbox.Options{MaxCalls: 3})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer explicit.Close()
	if !strings.Contains(explicit.Name(), "3 call budget") {
		t.Fatalf("Name() = %q, want the explicit budget to win", explicit.Name())
	}
}

func TestNewRejectsUnparseableBounds(t *testing.T) {
	t.Setenv("SANDBOX_TIMEOUT", "soon")
	if _, err := sandbox.New(sandbox.Options{}); err == nil {
		t.Fatal("New returned no error for an unparseable SANDBOX_TIMEOUT")
	}
}

func TestRunComposesCapabilityCalls(t *testing.T) {
	mode := newFake()
	source := `
		const { todos } = codemode.execute("todos.list", {});
		const first = todos.find(todo => !todo.completed);
		const done = codemode.execute("todos.complete", { id: first.id });
		return done.todo.title;
	`

	value, err := sandbox.Run(context.Background(), mode, source, sandbox.Options{Timeout: options})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if value != "write docs" {
		t.Fatalf("value = %#v, want %q", value, "write docs")
	}

	want := []string{`todos.list({})`, `todos.complete({"id":1})`}
	if len(mode.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", mode.calls, want)
	}
	for index, call := range want {
		if mode.calls[index] != call {
			t.Fatalf("call %d = %q, want %q", index, mode.calls[index], call)
		}
	}
}

func TestRunReportsUnregisteredCapability(t *testing.T) {
	mode := newFake()
	source := `return codemode.execute("todos.purge", {});`

	if _, err := sandbox.Run(context.Background(), mode, source, sandbox.Options{Timeout: options}); err == nil {
		t.Fatal("Run returned no error for an unregistered capability")
	} else if !strings.Contains(err.Error(), "todos.purge") {
		t.Fatalf("error = %v, want it to name the capability", err)
	}
}

func TestRunEnforcesCallBudget(t *testing.T) {
	mode := newFake()
	source := `
		for (let index = 0; index < 100; index++) {
			codemode.execute("todos.list", {});
		}
		return "unreachable";
	`

	if _, err := sandbox.Run(context.Background(), mode, source, sandbox.Options{Timeout: options, MaxCalls: 3}); err == nil {
		t.Fatal("Run returned no error for an exhausted call budget")
	}
	if len(mode.calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(mode.calls))
	}
}

func TestRunStopsRunawayProgram(t *testing.T) {
	mode := newFake()

	_, err := sandbox.Run(context.Background(), mode, `while (true) {}`, sandbox.Options{Timeout: 50 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestPromptRendersSelectedCapabilities(t *testing.T) {
	matches := []capability.Match{{Capability: capability.Capability{
		Name:        "todos.complete",
		Description: "Mark a todo as complete",
		Input:       capability.TypeSchema{Type: "CompleteRequest", Fields: map[string]string{"id": "number"}},
		Output:      capability.TypeSchema{Type: "CompleteResponse", Fields: map[string]string{"todo": "Todo"}},
	}}}

	t.Setenv("SANDBOX_ENGINE", "goja")
	runner, err := sandbox.New(sandbox.Options{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer runner.Close()

	want := "/** Mark a todo as complete */\n" +
		"codemode.execute(name: \"todos.complete\", input: { id: number }): { todo: Todo };\n\n"
	if got := runner.Prompt(matches); !strings.Contains(got, want) {
		t.Fatalf("Prompt() is missing the declaration %q:\n%s", want, got)
	}
}

// The bridge must satisfy the public interface it is handed.
var _ codemode.CodeMode = (*fake)(nil)
