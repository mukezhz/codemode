package capability

import (
	"context"
	"testing"
)

func TestKeywordRanksMatchingCapabilitiesFirst(t *testing.T) {
	registry := Registry{Capabilities: []Capability{
		{Name: "email.send", Description: "Sends an email."},
		{Name: "todos.create", Description: "Creates a todo item."},
		{Name: "todos.complete", Description: "Completes a todo item."},
	}}
	matches, err := Keyword(registry)(context.Background(), "create and complete todo", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2", len(matches))
	}
	for _, match := range matches {
		if match.Capability.Name == "email.send" {
			t.Fatalf("matches = %#v", matches)
		}
	}
}
