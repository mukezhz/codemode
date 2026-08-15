package registry_test

import (
	"encoding/json"
	"testing"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/registry"

	todov1 "github.com/mukezhz/codemode/gen/todo/v1"

	// Linked, so FromGlobalFiles can see them. FromFiles above cannot: it
	// scans only what it is handed.
	_ "github.com/mukezhz/codemode/gen/email/v1"
	_ "github.com/mukezhz/codemode/gen/invoice/v1"
	_ "github.com/mukezhz/codemode/gen/student/v1"
	_ "github.com/mukezhz/codemode/gen/task/v1"
	_ "github.com/mukezhz/codemode/gen/user/v1"
)

// FromFiles is the short path: generated code already carries its descriptors,
// so a consumer needs no descriptor set, no JSON, and no build step.
func TestFromFilesBuildsTheSameRegistryAsTheGenerator(t *testing.T) {
	built, err := registry.FromFiles(todov1.File_todo_proto)
	if err != nil {
		t.Fatalf("FromFiles returned error: %v", err)
	}
	if len(built.Capabilities) != 3 {
		t.Fatalf("capabilities = %d, want 3: %+v", len(built.Capabilities), built.Capabilities)
	}

	byName := map[string]string{}
	for _, item := range built.Capabilities {
		byName[item.Name] = item.RPC
	}
	for name, rpc := range map[string]string{
		"todos.create":   "todo.v1.TodoService/Create",
		"todos.list":     "todo.v1.TodoService/List",
		"todos.complete": "todo.v1.TodoService/Complete",
	} {
		if byName[name] != rpc {
			t.Errorf("%s = %q, want %q", name, byName[name], rpc)
		}
	}
}

// The descriptions and schemas are the whole point — search matches on one and
// the model reads the other.
func TestFromFilesCarriesDescriptionsAndSchemas(t *testing.T) {
	built, err := registry.FromFiles(todov1.File_todo_proto)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range built.Capabilities {
		if item.Description == "" {
			t.Errorf("%s has no description", item.Name)
		}
		if item.Name == "todos.list" {
			if got := item.Output.Fields["todos"]; got != "repeated Todo" {
				t.Errorf("todos.list output = %q, want %q", got, "repeated Todo")
			}
		}
		if item.Name == "todos.complete" {
			if got := item.Input.Fields["id"]; got != "number" {
				t.Errorf("todos.complete input id = %q, want %q", got, "number")
			}
		}
	}
}

// FromFiles scans only what it is handed, which is what makes it the safer
// habit: a new transitive dependency cannot widen the registry.
func TestFromFilesScansOnlyTheFilesGiven(t *testing.T) {
	built, err := registry.FromFiles(todov1.File_todo_proto)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range built.Capabilities {
		if item.Name[:indexDot(item.Name)] != "todos" {
			t.Fatalf("FromFiles returned %q from a file it was not given", item.Name)
		}
	}
}

// Every linked file is scanned, but only annotated services contribute.
func TestFromGlobalFilesFindsEveryAnnotatedService(t *testing.T) {
	built, err := registry.FromGlobalFiles()
	if err != nil {
		t.Fatalf("FromGlobalFiles returned error: %v", err)
	}
	namespaces := map[string]bool{}
	for _, item := range built.Capabilities {
		namespaces[item.Name[:indexDot(item.Name)]] = true
	}
	for _, want := range []string{"todos", "students", "invoices", "email"} {
		if !namespaces[want] {
			t.Errorf("namespace %q is missing from %v", want, namespaces)
		}
	}
}

func TestFromFilesRejectsNoInput(t *testing.T) {
	if _, err := registry.FromFiles(); err == nil {
		t.Fatal("FromFiles accepted no descriptors")
	}
}

func indexDot(name string) int {
	for i := range name {
		if name[i] == '.' {
			return i
		}
	}
	return len(name)
}

// The two paths must produce identical registries. If they drift, a project
// that generates registry.json in CI and one that calls FromFiles at startup
// would expose different capabilities from the same protos.
func TestFromFilesMatchesTheGeneratedRegistry(t *testing.T) {
	built, err := registry.FromGlobalFiles()
	if err != nil {
		t.Fatal(err)
	}
	generated, err := capability.LoadFile("registry.json")
	if err != nil {
		t.Skipf("registry.json is not generated: %v", err)
	}

	// Compared by name rather than as one blob: the two paths visit files in
	// different orders, and order is not part of what a registry means.
	reflected := byName(t, built)
	fromFile := byName(t, generated)
	if len(reflected) != len(fromFile) {
		t.Fatalf("capability counts differ: reflection %d, registry.json %d",
			len(reflected), len(fromFile))
	}
	for name, want := range fromFile {
		got, found := reflected[name]
		if !found {
			t.Errorf("%s is in registry.json but not in the reflected registry", name)
			continue
		}
		if got != want {
			t.Errorf("%s differs:\n reflection: %s\nregistry.json: %s", name, got, want)
		}
	}
}

func byName(t *testing.T, built capability.Registry) map[string]string {
	t.Helper()
	byName := make(map[string]string, len(built.Capabilities))
	for _, item := range built.Capabilities {
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		byName[item.Name] = string(encoded)
	}
	return byName
}

// The same binary must produce the same registry every run, or the prompt built
// from it stops being cacheable. RangeFiles walks a map, so this is a real risk.
func TestFromGlobalFilesIsDeterministic(t *testing.T) {
	first, err := registry.FromGlobalFiles()
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		next, err := registry.FromGlobalFiles()
		if err != nil {
			t.Fatal(err)
		}
		for index := range first.Capabilities {
			if first.Capabilities[index].Name != next.Capabilities[index].Name {
				t.Fatalf("capability %d moved: %q then %q",
					index, first.Capabilities[index].Name, next.Capabilities[index].Name)
			}
		}
	}
}
