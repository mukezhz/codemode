package registry_test

import (
	"encoding/json"
	"testing"

	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/registry"

	todov1 "github.com/mukezhz/code-mode/gen/todo/v1"

	// Linked, so FromGlobalFiles can see them. FromFiles above cannot: it
	// scans only what it is handed.
	_ "github.com/mukezhz/code-mode/gen/email/v1"
	_ "github.com/mukezhz/code-mode/gen/invoice/v1"
	_ "github.com/mukezhz/code-mode/gen/student/v1"
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

	fromReflection, err := json.Marshal(built)
	if err != nil {
		t.Fatal(err)
	}
	fromDescriptorSet, err := json.Marshal(generated)
	if err != nil {
		t.Fatal(err)
	}
	if string(fromReflection) != string(fromDescriptorSet) {
		t.Fatalf("the two paths disagree:\nreflection:  %s\ndescriptors: %s",
			fromReflection, fromDescriptorSet)
	}
}
