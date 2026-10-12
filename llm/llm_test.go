package llm

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// Test-only providers, so selection is exercised without a vendor SDK.
type stub struct{ name string }

func (stub *stub) Complete(context.Context, string, string) (string, error) { return "", nil }
func (stub *stub) Name() string                                             { return stub.name }

func init() {
	RegisterProvider(Provider{
		Name:     "first",
		Aliases:  []string{"primero"},
		New:      func(Options) (Client, error) { return &stub{name: "first"}, nil },
		FromEnv:  func() (Options, bool, error) { return Options{Model: "from-env"}, os.Getenv("STUB_FIRST") != "", nil },
		Priority: 1,
	})
	RegisterProvider(Provider{
		Name:     "second",
		New:      func(Options) (Client, error) { return &stub{name: "second"}, nil },
		FromEnv:  func() (Options, bool, error) { return Options{}, os.Getenv("STUB_SECOND") != "", nil },
		Priority: 2,
	})
	RegisterProvider(Provider{
		Name: "manual",
		New:  func(Options) (Client, error) { return &stub{name: "manual"}, nil },
	})
}

func TestNewSelectsARegisteredProvider(t *testing.T) {
	for _, name := range []string{"first", "primero", "manual"} {
		clearEnv(t)
		client, err := New(Options{Provider: name})
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

func TestNewDoesNotConsultTheEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv("LLM_PROVIDER", "first")
	t.Setenv("STUB_FIRST", "yes")

	if _, err := New(Options{}); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("error = %v, want ErrNoProvider — New read LLM_PROVIDER", err)
	}
}

func TestOptionsFromEnvPrefersTheLowestPriorityConfiguredProvider(t *testing.T) {
	clearEnv(t)
	t.Setenv("STUB_FIRST", "yes")
	t.Setenv("STUB_SECOND", "yes")
	options, err := OptionsFromEnv()
	if err != nil {
		t.Fatalf("OptionsFromEnv returned error: %v", err)
	}
	if options.Provider != "first" {
		t.Fatalf("Provider = %q, want the lower-priority provider", options.Provider)
	}
}

func TestOptionsFromEnvNeverSelfSelectsAProviderWithoutDetection(t *testing.T) {
	clearEnv(t)
	if _, err := OptionsFromEnv(); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("error = %v, want ErrNoProvider", err)
	}
}

func TestOptionsFromEnvReturnsAnOverridableValue(t *testing.T) {
	clearEnv(t)
	t.Setenv("STUB_FIRST", "yes")
	t.Setenv("LLM_MAX_TOKENS", "4321")

	options, err := OptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if options.Model != "from-env" || options.MaxTokens != 4321 {
		t.Fatalf("options = %+v, want the environment values", options)
	}

	options.Model = "mine"
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "first" {
		t.Fatalf("Name() = %q", client.Name())
	}
}

func TestOptionsFromEnvRejectsUnparseableValues(t *testing.T) {
	for name, value := range map[string]string{
		"LLM_MAX_TOKENS":  "lots",
		"LLM_TEMPERATURE": "warm",
		"LLM_TOP_P":       "most",
		"LLM_TIMEOUT":     "soon",
	} {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("STUB_FIRST", "yes")
			t.Setenv(name, value)
			if _, err := OptionsFromEnv(); err == nil {
				t.Fatalf("OptionsFromEnv accepted %s=%q", name, value)
			}
		})
	}
}

func TestNewNamesTheMissingImportForAShippedProvider(t *testing.T) {
	for provider, want := range map[string]string{
		ProviderClaude: "llm/claude",
		ProviderOpenAI: "llm/openai",
	} {
		t.Run(provider, func(t *testing.T) {
			clearEnv(t)
			_, err := New(Options{Provider: provider})
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
	_, err := New(Options{Provider: "llama"})
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

func TestOptionsFromEnvLayersTheSharedBaseURL(t *testing.T) {
	clearEnv(t)
	t.Setenv("STUB_FIRST", "yes")
	t.Setenv("LLM_BASE_URL", "http://shared")

	options, err := OptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if options.BaseURL != "http://shared" {
		t.Fatalf("BaseURL = %q, want the shared value", options.BaseURL)
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
