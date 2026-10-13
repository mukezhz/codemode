package enginetest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/sandbox"
	"github.com/mukezhz/codemode/sandbox/goja"
	"github.com/mukezhz/codemode/sandbox/lua"
	"github.com/mukezhz/codemode/sandbox/starlark"
	"github.com/mukezhz/codemode/sandbox/subprocess"
)

type engine struct {
	name string
	new  func(sandbox.Options) (sandbox.Runner, error)

	compose      string
	unregistered string
	budget       string
	forever      string
	nothing      string
	bomb         string
	isolated     bool
}

var worker []string

// TestMain builds codemode-worker once so every engine test also runs out of process.
// CODEMODE_TEST_WORKER replaces it with another argv, such as a docker run.
func TestMain(m *testing.M) {
	if argv := strings.Fields(os.Getenv("CODEMODE_TEST_WORKER")); len(argv) > 0 {
		worker = argv
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "enginetest-")
	if err != nil {
		panic(err)
	}
	worker = []string{filepath.Join(dir, "codemode-worker")}
	build := exec.Command("go", "build", "-o", worker[0], "github.com/mukezhz/codemode/cmd/codemode-worker")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// dialect is the in-process engine whose prompt a variant shares.
func (engine engine) dialect() string { return strings.TrimPrefix(engine.name, "subprocess-") }

func engines() []engine {
	inProcess := inProcessEngines()
	all := inProcess
	for _, inner := range inProcess {
		variant := inner
		variant.name = "subprocess-" + inner.name
		variant.isolated = true
		variant.new = func(options sandbox.Options) (sandbox.Runner, error) {
			options.Engine = variant.name
			options.Worker = worker
			options.WorkerIsolated = os.Getenv("CODEMODE_TEST_WORKER") != ""
			return subprocess.New(options)
		}
		all = append(all, variant)
	}
	return all
}

func inProcessEngines() []engine {
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
			bomb:    `const a = []; while (true) { a.push(new Array(1e6).fill(1)); }`,
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
			// Starlark has no while loop; a long range is its runaway shape.
			forever: `total = 0
for i in range(100000000):
    total = total + i
return total`,
			nothing: `x = 1`,
			bomb: `a = []
for i in range(100000000):
    a.append([i] * 1000000)
return len(a)`,
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
			bomb: `local a = {}
while true do
  local t = {}
  for i = 1, 1000000 do t[i] = i end
  a[#a + 1] = t
end`,
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
			for _, fragment := range want[engine.dialect()] {
				if !strings.Contains(prompt, fragment) {
					t.Fatalf("prompt is missing %q:\n%s", fragment, prompt)
				}
			}
			if _, err := runner.Run(context.Background(), newFake(), exampleOf(t, runner)); err != nil {
				t.Fatalf("the dialect's own example does not run: %v", err)
			}
		})
	}
}

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
			for _, fragment := range want[engine.dialect()] {
				if !strings.Contains(prompt, fragment) {
					t.Errorf("prompt is missing %q:\n%s", fragment, prompt)
				}
			}
		})
	}
}

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
		"":           "javascript",
	} {
		t.Run("alias="+alias, func(t *testing.T) {
			runner, err := sandbox.New(sandbox.Options{Engine: alias})
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

var (
	_ func(sandbox.Options) (sandbox.Runner, error) = goja.New
	_ func(sandbox.Options) (sandbox.Runner, error) = starlark.New
	_ func(sandbox.Options) (sandbox.Runner, error) = lua.New
)

func TestEnginesDeclareReferencedShapes(t *testing.T) {
	matches := []capability.Match{{Capability: capability.Capability{
		Name:        "reports.byProject",
		Description: "Counts tasks per project",
		Input:       capability.TypeSchema{Fields: map[string]string{}},
		Output:      capability.TypeSchema{Fields: map[string]string{"groups": "repeated GroupCount"}},
		Types: map[string]capability.TypeSchema{
			"GroupCount": {Type: "GroupCount", Fields: map[string]string{
				"name": "string", "open": "number",
			}},
		},
	}}}

	want := map[string]string{
		"goja":     "type GroupCount = { name: string, open: number };",
		"starlark": `# GroupCount = {"name": string, "open": int|float}`,
		"lua":      "-- GroupCount = { name = string, open = number }",
	}

	for _, engine := range engines() {
		t.Run(engine.name, func(t *testing.T) {
			runner, err := engine.new(sandbox.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()

			prompt := runner.Prompt(matches)
			if !strings.Contains(prompt, want[engine.dialect()]) {
				t.Fatalf("prompt is missing %q:\n%s", want[engine.dialect()], prompt)
			}
		})
	}
}

func TestEnginesOmitTheShapesSectionWhenEmpty(t *testing.T) {
	matches := []capability.Match{{Capability: capability.Capability{
		Name:        "tasks.count",
		Description: "Counts tasks",
		Input:       capability.TypeSchema{Fields: map[string]string{}},
		Output:      capability.TypeSchema{Fields: map[string]string{"count": "number"}},
	}}}

	for _, engine := range engines() {
		runner, err := engine.new(sandbox.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if prompt := runner.Prompt(matches); strings.Contains(prompt, "Shapes referred to above") {
			t.Errorf("%s emitted an empty shapes section", engine.name)
		}
		runner.Close()
	}
}

func TestEnginesRejectOversizedResponses(t *testing.T) {
	for _, engine := range engines() {
		t.Run(engine.name, func(t *testing.T) {
			runner, err := engine.new(sandbox.Options{Timeout: 5 * time.Second, MaxResponseBytes: 10})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()

			_, err = runner.Run(context.Background(), newFake(), engine.compose)
			if err == nil || !strings.Contains(err.Error(), "byte limit") {
				t.Fatalf("err = %v, want the response limit", err)
			}
		})
	}
}

func TestEnginesSurviveMemoryBombs(t *testing.T) {
	for _, engine := range engines() {
		t.Run(engine.name, func(t *testing.T) {
			if !engine.isolated {
				t.Skip("in-process engines share the host heap; only subprocess engines cap memory")
			}
			runner, err := engine.new(sandbox.Options{Timeout: 20 * time.Second, MaxHeapBytes: 64 << 20})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()

			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, err = runner.Run(context.Background(), newFake(), engine.bomb)
			runtime.ReadMemStats(&after)
			if err == nil || !strings.Contains(err.Error(), "memory limit") {
				t.Fatalf("err = %v, want the memory limit", err)
			}
			if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > 32<<20 {
				t.Fatalf("host heap grew by %d bytes", grown)
			}
		})
	}
}

func TestEnginesRejectHugeResults(t *testing.T) {
	programs := map[string]string{
		"goja":     `return "a".repeat(2 * 1024 * 1024);`,
		"starlark": `return "a" * (2 * 1024 * 1024)`,
		"lua":      `return string.rep("a", 2 * 1024 * 1024)`,
	}
	for _, engine := range engines() {
		t.Run(engine.name, func(t *testing.T) {
			if !engine.isolated {
				t.Skip("in-process engines hand the value over in memory")
			}
			runner, err := engine.new(sandbox.Options{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close()

			_, err = runner.Run(context.Background(), newFake(), programs[engine.dialect()])
			if err == nil || !strings.Contains(err.Error(), "size limit") {
				t.Fatalf("err = %v, want the size limit", err)
			}
		})
	}
}
