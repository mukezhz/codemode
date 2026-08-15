package llm

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// Two providers registered only by this test, so the selection rules can be
// exercised without linking a vendor SDK.
type stub struct{ name string }

func (stub *stub) Complete(context.Context, string, string) (string, error) { return "", nil }
func (stub *stub) Name() string                                             { return stub.name }

func init() {
	RegisterProvider(Provider{
		Name:       "first",
		Aliases:    []string{"primero"},
		New:        func() (Client, error) { return &stub{name: "first"}, nil },
		Configured: func() bool { return os.Getenv("STUB_FIRST") != "" },
		Priority:   1,
	})
	RegisterProvider(Provider{
		Name:       "second",
		New:        func() (Client, error) { return &stub{name: "second"}, nil },
		Configured: func() bool { return os.Getenv("STUB_SECOND") != "" },
		Priority:   2,
	})
	// No Configured func: selectable by name, never self-selected.
	RegisterProvider(Provider{
		Name: "manual",
		New:  func() (Client, error) { return &stub{name: "manual"}, nil },
	})
}

func TestNewSelectsARegisteredProvider(t *testing.T) {
	for _, name := range []string{"first", "primero", "manual"} {
		clearEnv(t)
		t.Setenv("LLM_PROVIDER", name)
		client, err := New()
		if err != nil {
			t.Fatalf("New(%q) returned error: %v", name, err)
		}
		want := name
		if name == "primero" {
			want = "first"
		}
		if client.Name() != want {
			t.Fatalf("Name() = %q, want %q", client.Name(), want)
		}
	}
}

// Priority, not import order, decides which configured provider wins.
func TestNewPrefersTheLowestPriorityConfiguredProvider(t *testing.T) {
	clearEnv(t)
	t.Setenv("STUB_FIRST", "yes")
	t.Setenv("STUB_SECOND", "yes")
	client, err := New()
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if client.Name() != "first" {
		t.Fatalf("Name() = %q, want the lower-priority provider", client.Name())
	}
}

// A provider with no Configured func must never be picked implicitly, or
// registering one would change which provider an existing deployment uses.
func TestNewNeverSelfSelectsAProviderWithoutDetection(t *testing.T) {
	clearEnv(t)
	if _, err := New(); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("error = %v, want ErrNoProvider", err)
	}
}

// A missing blank import is the common failure of this design, so the error
// has to name the package rather than say "unsupported".
func TestNewNamesTheMissingImportForAShippedProvider(t *testing.T) {
	for provider, want := range map[string]string{
		ProviderClaude: "llm/claude",
		ProviderOpenAI: "llm/openai",
	} {
		t.Run(provider, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("LLM_PROVIDER", provider)
			_, err := New()
			if err == nil {
				t.Fatal("New unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want it to name %q", err, want)
			}
		})
	}
}

func TestNewRejectsAnUnknownProvider(t *testing.T) {
	clearEnv(t)
	t.Setenv("LLM_PROVIDER", "llama")
	err := func() error { _, err := New(); return err }()
	if err == nil {
		t.Fatal("New returned no error for an unknown provider")
	}
	if errors.Is(err, ErrNoProvider) {
		t.Fatalf("error = %v, want an unknown-provider error", err)
	}
	if strings.Contains(err.Error(), "llm/llama") {
		t.Fatalf("error = %v, want it to not invent an import path", err)
	}
}

func TestBaseURLFallsBackToTheSharedVariable(t *testing.T) {
	clearEnv(t)
	t.Setenv("LLM_BASE_URL", "http://shared")
	if got := BaseURL("OPENAI_BASE_URL"); got != "http://shared" {
		t.Fatalf("BaseURL() = %q, want the shared fallback", got)
	}
	t.Setenv("OPENAI_BASE_URL", "http://specific")
	if got := BaseURL("OPENAI_BASE_URL"); got != "http://specific" {
		t.Fatalf("BaseURL() = %q, want the specific value to win", got)
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"LLM_PROVIDER", "LLM_BASE_URL", "STUB_FIRST", "STUB_SECOND",
		"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL",
		"OPENAI_API_KEY", "OPENAI_BASE_URL",
	} {
		t.Setenv(name, "")
	}
}
