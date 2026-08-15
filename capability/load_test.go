package capability_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mukezhz/code-mode/capability"
)

const registryJSON = `{"capabilities":[{"name":"todos.list","rpc":"todo.v1.TodoService/List",` +
	`"description":"Lists todos.","input":{"type":"ListTodosRequest","fields":{}},` +
	`"output":{"type":"ListTodosResponse","fields":{"todos":"repeated Todo"}}}]}`

func TestLoadDecodesARegistry(t *testing.T) {
	registry, err := capability.Load(strings.NewReader(registryJSON))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(registry.Capabilities) != 1 || registry.Capabilities[0].RPC != "todo.v1.TodoService/List" {
		t.Fatalf("registry = %#v", registry)
	}
}

func TestLoadFSReadsAnEmbeddedRegistry(t *testing.T) {
	filesystem := fstest.MapFS{"registry.json": {Data: []byte(registryJSON)}}
	if _, err := capability.LoadFS(filesystem, "registry.json"); err != nil {
		t.Fatalf("LoadFS returned error: %v", err)
	}
}

// An empty registry means every capability call will be rejected, which is a
// configuration mistake worth catching at startup rather than per request.
func TestLoadRejectsAnEmptyRegistry(t *testing.T) {
	if _, err := capability.Load(strings.NewReader(`{"capabilities":[]}`)); err == nil {
		t.Fatal("Load accepted a registry with no capabilities")
	}
}

// A typo in a hand-edited registry is silent without this.
func TestLoadRejectsUnknownFields(t *testing.T) {
	if _, err := capability.Load(strings.NewReader(`{"capabilities":[],"oops":1}`)); err == nil {
		t.Fatal("Load accepted an unknown field")
	}
}
