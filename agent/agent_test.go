package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/codemode"
	"github.com/mukezhz/code-mode/llm"
	"github.com/mukezhz/code-mode/sandbox"

	// Run builds an engine from the environment when Options.Runner is nil.
	_ "github.com/mukezhz/code-mode/sandbox/goja"
)

// mode answers searches and capability calls from a fixed script.
type mode struct {
	calls []string
}

func (mode *mode) Search(_ context.Context, _ string, limit int) ([]capability.Match, error) {
	item := capability.Capability{
		Name:        "todos.list",
		RPC:         "todo.v1.TodoService/List",
		Description: "Lists all todo items",
		Output:      capability.TypeSchema{Type: "ListResponse", Fields: map[string]string{"todos": "Todo[]"}},
	}
	if limit < 1 {
		return nil, errors.New("limit must be positive")
	}
	return []capability.Match{{Capability: item, Distance: 0}}, nil
}

func (mode *mode) Execute(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
	mode.calls = append(mode.calls, name)
	if name != "todos.list" {
		return nil, errors.New("capability \"" + name + "\" is not registered")
	}
	return json.RawMessage(`{"todos":[{"id":1,"title":"write docs"}]}`), nil
}

// model returns queued responses in order and records the prompts it received.
type model struct {
	responses []string
	prompts   []string
	systems   []string
}

func (model *model) Name() string { return "test/model" }

func (model *model) Complete(_ context.Context, system, user string) (string, error) {
	model.systems = append(model.systems, system)
	model.prompts = append(model.prompts, user)
	if len(model.responses) == 0 {
		return "", errors.New("no responses left")
	}
	response := model.responses[0]
	model.responses = model.responses[1:]
	return response, nil
}

const workingProgram = `const { todos } = codemode.execute("todos.list", {}); return todos[0].title;`

func TestRunReturnsTheProgramsAnswer(t *testing.T) {
	runtime := &mode{}
	author := &model{responses: []string{workingProgram}}

	result, err := Run(context.Background(), runtime, author, "list my todos", Options{})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Answer != "write docs" {
		t.Fatalf("answer = %#v, want %q", result.Answer, "write docs")
	}
	if result.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", result.Attempts)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(result.Matches))
	}
}

func TestRunRepairsAFailedProgram(t *testing.T) {
	runtime := &mode{}
	author := &model{responses: []string{
		`return codemode.execute("todos.purge", {});`, // not registered
		workingProgram,
	}}

	result, err := Run(context.Background(), runtime, author, "list my todos", Options{})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", result.Attempts)
	}
	if len(author.prompts) != 2 {
		t.Fatalf("prompts = %d, want 2", len(author.prompts))
	}
	// The second prompt must carry the failure so the model can correct it.
	if !strings.Contains(author.prompts[1], "todos.purge") {
		t.Fatalf("repair prompt does not include the failed program: %q", author.prompts[1])
	}
}

func TestRunGivesUpAfterAttempts(t *testing.T) {
	runtime := &mode{}
	broken := `return codemode.execute("todos.purge", {});`
	author := &model{responses: []string{broken, broken}}

	result, err := Run(context.Background(), runtime, author, "list my todos", Options{Attempts: 2})
	if err == nil {
		t.Fatal("Run returned no error for a program that never succeeds")
	}
	if result.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", result.Attempts)
	}
}

func TestProgramStripsMarkdownFences(t *testing.T) {
	for name, response := range map[string]string{
		"tagged": "```javascript\nreturn 1;\n```",
		"bare":   "```\nreturn 1;\n```",
		"none":   "return 1;",
		"padded": "  ```js\nreturn 1;\n```  ",
	} {
		if got := Program(response); got != "return 1;" {
			t.Fatalf("%s: Program() = %q, want %q", name, got, "return 1;")
		}
	}
}

func TestAnswerRendersValues(t *testing.T) {
	if got := Answer(nil); got != "(no value returned)" {
		t.Fatalf("Answer(nil) = %q", got)
	}
	if got := Answer("done"); got != "done" {
		t.Fatalf("Answer(string) = %q", got)
	}
	if got := Answer(map[string]any{"ok": true}); got != `{"ok":true}` {
		t.Fatalf("Answer(map) = %q", got)
	}
}

// runner records the programs it was asked to evaluate and returns a fixed
// answer, so the agent's use of the sandbox seam is observable.
type runner struct {
	sources []string
	closed  bool
}

func (runner *runner) Name() string     { return "test/runner" }
func (runner *runner) Language() string { return "test" }

func (runner *runner) Prompt(matches []capability.Match) string {
	return fmt.Sprintf("test prompt with %d capabilities", len(matches))
}

func (runner *runner) Run(_ context.Context, _ codemode.CodeMode, source string) (any, error) {
	runner.sources = append(runner.sources, source)
	return "ran " + source, nil
}

func (runner *runner) Close() error {
	runner.closed = true
	return nil
}

func TestRunUsesTheProvidedRunner(t *testing.T) {
	engine := &runner{}
	author := &model{responses: []string{workingProgram}}

	result, err := Run(context.Background(), &mode{}, author, "list my todos", Options{Runner: engine})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(engine.sources) != 1 || engine.sources[0] != workingProgram {
		t.Fatalf("runner saw %#v, want the generated program", engine.sources)
	}
	if result.Answer != "ran "+workingProgram {
		t.Fatalf("answer = %#v, want the runner's value", result.Answer)
	}
	// A caller-supplied runner is the caller's to close.
	if engine.closed {
		t.Fatal("Run closed a runner it did not build")
	}
}

// The fakes must satisfy the interfaces the agent depends on.
var (
	_ codemode.CodeMode = (*mode)(nil)
	_ llm.Client        = (*model)(nil)
	_ sandbox.Runner    = (*runner)(nil)
)

// Instructions must reach the model, and must not displace the engine's own
// rules — a prompt without them produces code the sandbox cannot run.
func TestInstructionsAreAppendedToTheEnginePrompt(t *testing.T) {
	runtime := &mode{}
	author := &model{responses: []string{workingProgram}}

	_, err := Run(context.Background(), runtime, author, "list my todos", Options{
		Instructions: "Always answer in French.",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(author.systems) != 1 {
		t.Fatalf("systems = %d, want 1", len(author.systems))
	}
	system := author.systems[0]
	if !strings.Contains(system, "Always answer in French.") {
		t.Fatalf("system prompt is missing the instructions:\n%s", system)
	}
	if !strings.Contains(system, "codemode.execute") {
		t.Fatalf("instructions displaced the engine's own rules:\n%s", system)
	}
	// The engine's rules come first; instructions qualify them.
	if strings.Index(system, "codemode.execute") > strings.Index(system, "Always answer in French.") {
		t.Fatalf("instructions were prepended, not appended:\n%s", system)
	}
}

// Blank instructions must not add an empty section to a cached prompt.
func TestBlankInstructionsLeaveThePromptUnchanged(t *testing.T) {
	plain := &model{responses: []string{workingProgram}}
	if _, err := Run(context.Background(), &mode{}, plain, "list my todos", Options{}); err != nil {
		t.Fatal(err)
	}
	spaced := &model{responses: []string{workingProgram}}
	if _, err := Run(context.Background(), &mode{}, spaced, "list my todos", Options{Instructions: "  \n "}); err != nil {
		t.Fatal(err)
	}
	if plain.systems[0] != spaced.systems[0] {
		t.Fatal("blank instructions changed the system prompt")
	}
}
