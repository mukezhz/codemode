package search

import (
	"context"
	"testing"

	"github.com/mukezhz/codemode/capability"
)

func TestTodoCapabilitiesRankFirst(t *testing.T) {
	registry := capability.Registry{Capabilities: []capability.Capability{
		{Name: "email.send", Description: "Sends an email."},
		{Name: "todos.create", Description: "Creates a todo item."},
		{Name: "todos.complete", Description: "Completes a todo item."},
	}}
	matches, err := New(registry)(context.Background(), "create and complete todo", 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range matches {
		if match.Capability.Name == "email.send" {
			t.Fatalf("matches = %#v", matches)
		}
	}
}
