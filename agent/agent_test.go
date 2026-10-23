package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/program"
	"github.com/mukezhz/codemode/sandbox"

	"github.com/mukezhz/codemode/sandbox/goja"
)

type mode struct {
	calls []string
}

func (mode *mode) Search(_ context.Context, _ string, limit int) ([]capability.Match, error) {
	item := capability.Capability{
		Name:        "todos.list",
		RPC:         "todo.v1.TodoService/List",
		Description: "Lists all todo items",
		ReadOnly:    true,
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

	result, err := Run(context.Background(), runtime, author, "list my todos", Options{Runner: newRunner(t)})
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

	result, err := Run(context.Background(), runtime, author, "list my todos", Options{Runner: newRunner(t)})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", result.Attempts)
	}
	if len(author.prompts) != 2 {
		t.Fatalf("prompts = %d, want 2", len(author.prompts))
	}
	if !strings.Contains(author.prompts[1], "todos.purge") {
		t.Fatalf("repair prompt does not include the failed program: %q", author.prompts[1])
	}
}

func TestRunGivesUpAfterAttempts(t *testing.T) {
	runtime := &mode{}
	broken := `return codemode.execute("todos.purge", {});`
	author := &model{responses: []string{broken, broken}}

	result, err := Run(context.Background(), runtime, author, "list my todos", Options{Attempts: 2, Runner: newRunner(t)})
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
	if engine.closed {
		t.Fatal("Run closed a runner it did not build")
	}
}

var (
	_ codemode.CodeMode = (*mode)(nil)
	_ llm.Client        = (*model)(nil)
	_ sandbox.Runner    = (*runner)(nil)
)

func TestInstructionsAreAppendedToTheEnginePrompt(t *testing.T) {
	runtime := &mode{}
	author := &model{responses: []string{workingProgram}}

	_, err := Run(context.Background(), runtime, author, "list my todos", Options{
		Runner:       newRunner(t),
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
	if strings.Index(system, "codemode.execute") > strings.Index(system, "Always answer in French.") {
		t.Fatalf("instructions were prepended, not appended:\n%s", system)
	}
}

func TestBlankInstructionsLeaveThePromptUnchanged(t *testing.T) {
	plain := &model{responses: []string{workingProgram}}
	if _, err := Run(context.Background(), &mode{}, plain, "list my todos", Options{Runner: newRunner(t)}); err != nil {
		t.Fatal(err)
	}
	spaced := &model{responses: []string{workingProgram}}
	if _, err := Run(context.Background(), &mode{}, spaced, "list my todos", Options{Runner: newRunner(t), Instructions: "  \n "}); err != nil {
		t.Fatal(err)
	}
	if plain.systems[0] != spaced.systems[0] {
		t.Fatal("blank instructions changed the system prompt")
	}
}

func newRunner(t *testing.T) sandbox.Runner {
	t.Helper()
	runner, err := goja.New(sandbox.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runner.Close() })
	return runner
}

func TestRunRequiresARunner(t *testing.T) {
	_, err := Run(context.Background(), &mode{}, &model{}, "list my todos", Options{})
	if err == nil {
		t.Fatal("Run accepted a nil Runner")
	}
	if !strings.Contains(err.Error(), "Runner is required") {
		t.Fatalf("error = %v, want it to name the missing option", err)
	}
}

type fingerprinted struct {
	*mode
	fingerprint string
}

func (runtime fingerprinted) Fingerprint() string { return runtime.fingerprint }

// exactEmbedder makes cache hits depend only on exact request text.
func exactEmbedder() capability.Embedder {
	return capability.Embedder{
		Model: "test/exact",
		Embed: func(_ context.Context, text string) ([]float32, error) {
			sum := sha256.Sum256([]byte(text))
			vector := make([]float32, len(sum))
			for i, b := range sum {
				vector[i] = float32(b)
			}
			return vector, nil
		},
	}
}

func newCache(t *testing.T) program.Cache {
	t.Helper()
	cache, err := program.New(program.NewMemoryStore(), exactEmbedder(), program.Options{})
	if err != nil {
		t.Fatalf("program.New returned error: %v", err)
	}
	t.Cleanup(func() { cache.Close() })
	return cache
}

func TestRunCachesTheProgramAndReusesIt(t *testing.T) {
	runtime := fingerprinted{mode: &mode{}, fingerprint: "registry-1"}
	author := &model{responses: []string{workingProgram}}
	options := Options{Runner: newRunner(t), Cache: newCache(t)}

	first, err := Run(context.Background(), runtime, author, "list my todos", options)
	if err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
	if first.Cached {
		t.Fatal("the first run reported a cache hit")
	}

	second, err := Run(context.Background(), runtime, author, "list my todos", options)
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if !second.Cached {
		t.Fatal("the second run generated a program instead of reusing the cached one")
	}
	if second.Answer != first.Answer {
		t.Fatalf("cached answer = %#v, want %#v", second.Answer, first.Answer)
	}
	if len(author.prompts) != 1 {
		t.Fatalf("model was called %d times, want 1", len(author.prompts))
	}
}

func TestRunDoesNotReuseAProgramAcrossCapabilitySets(t *testing.T) {
	cache := newCache(t)
	author := &model{responses: []string{workingProgram, workingProgram}}

	before := fingerprinted{mode: &mode{}, fingerprint: "registry-1"}
	if _, err := Run(context.Background(), before, author, "list my todos",
		Options{Runner: newRunner(t), Cache: cache}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	after := fingerprinted{mode: &mode{}, fingerprint: "registry-2"}
	result, err := Run(context.Background(), after, author, "list my todos",
		Options{Runner: newRunner(t), Cache: cache})
	if err != nil {
		t.Fatalf("Run after the registry changed returned error: %v", err)
	}
	if result.Cached {
		t.Fatal("a program written for another capability set was reused")
	}
}

func TestRunForgetsACachedProgramThatNoLongerRuns(t *testing.T) {
	ctx := context.Background()
	cache := newCache(t)
	runtime := fingerprinted{mode: &mode{}, fingerprint: "registry-1"}

	stale := program.Key{Request: "list my todos", Language: "javascript", Capabilities: "registry-1"}
	if err := cache.Put(ctx, stale, `return codemode.execute("todos.purge", {});`); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	author := &model{responses: []string{workingProgram}}
	result, err := Run(ctx, runtime, author, "list my todos", Options{Runner: newRunner(t), Cache: cache})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Cached {
		t.Fatal("a program that no longer runs was served from the cache")
	}
	if result.Answer != "write docs" {
		t.Fatalf("answer = %#v, want %q", result.Answer, "write docs")
	}

	entry, found, err := cache.Lookup(ctx, stale)
	if err != nil || !found {
		t.Fatalf("Lookup = (found %v, err %v), want (true, nil)", found, err)
	}
	if entry.Source != workingProgram {
		t.Fatalf("cached source = %q, want the program that succeeded", entry.Source)
	}
}

func TestRunWithoutAFingerprintDoesNotCache(t *testing.T) {
	author := &model{responses: []string{workingProgram, workingProgram}}
	options := Options{Runner: newRunner(t), Cache: newCache(t)}

	for attempt := 1; attempt <= 2; attempt++ {
		result, err := Run(context.Background(), &mode{}, author, "list my todos", options)
		if err != nil {
			t.Fatalf("run %d returned error: %v", attempt, err)
		}
		if result.Cached {
			t.Fatalf("run %d reported a cache hit without a capability fingerprint", attempt)
		}
	}
}

func TestRunReportsWhereTheTimeWent(t *testing.T) {
	runtime := fingerprinted{mode: &mode{}, fingerprint: "registry-1"}
	author := &model{responses: []string{workingProgram}}
	options := Options{Runner: newRunner(t), Cache: newCache(t)}

	generated, err := Run(context.Background(), runtime, author, "list my todos", options)
	if err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
	if generated.Timing.Total == 0 || generated.Timing.Generate == 0 || generated.Timing.Run == 0 {
		t.Fatalf("generated run timing = %s, want a model call and a sandbox run", generated.Timing)
	}
	if generated.Timing.Search == 0 {
		t.Fatalf("generated run timing = %s, want a capability search", generated.Timing)
	}

	cached, err := Run(context.Background(), runtime, author, "list my todos", options)
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if cached.Timing.Generate != 0 {
		t.Errorf("cache hit spent %s generating", cached.Timing.Generate)
	}
	if cached.Timing.Search != 0 {
		t.Errorf("cache hit spent %s searching", cached.Timing.Search)
	}
	if cached.Timing.Cache == 0 || cached.Timing.Run == 0 {
		t.Errorf("cache hit timing = %s, want a lookup and a sandbox run", cached.Timing)
	}
	if cached.Timing.Total == 0 {
		t.Error("cache hit reported no total")
	}
}

func TestRunReportsTimingWhenItGivesUp(t *testing.T) {
	broken := `return codemode.execute("todos.purge", {});`
	author := &model{responses: []string{broken, broken}}

	result, err := Run(context.Background(), &mode{}, author, "list my todos",
		Options{Attempts: 2, Runner: newRunner(t)})
	if err == nil {
		t.Fatal("Run returned no error for a program that never succeeds")
	}
	if result.Timing.Total == 0 || result.Timing.Generate == 0 || result.Timing.Run == 0 {
		t.Fatalf("failed run timing = %s, want the attempts it paid for", result.Timing)
	}
}

func TestRunTreatsAnEmptyFingerprintAsNoCache(t *testing.T) {
	runtime := fingerprinted{mode: &mode{}, fingerprint: ""}
	author := &model{responses: []string{workingProgram, workingProgram}}
	options := Options{Runner: newRunner(t), Cache: newCache(t)}

	for attempt := 1; attempt <= 2; attempt++ {
		result, err := Run(context.Background(), runtime, author, "list my todos", options)
		if err != nil {
			t.Fatalf("run %d returned error: %v", attempt, err)
		}
		if result.Cached {
			t.Fatalf("run %d cached against an empty fingerprint", attempt)
		}
	}
}

func TestRunDoesNotReuseAProgramAcrossInstructions(t *testing.T) {
	runtime := fingerprinted{mode: &mode{}, fingerprint: "registry-1"}
	author := &model{responses: []string{workingProgram, workingProgram}}
	cache := newCache(t)

	for _, instructions := range []string{"You are answering for alice.", "You are answering for bob."} {
		result, err := Run(context.Background(), runtime, author, "list my todos",
			Options{Runner: newRunner(t), Cache: cache, Instructions: instructions})
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		if result.Cached {
			t.Fatalf("a program written under other instructions was reused for %q", instructions)
		}
	}
}
