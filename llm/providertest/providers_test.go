// Package providertest holds the cross-provider behaviour tests. It lives
// beside the providers rather than inside llm so that package keeps its
// property of linking no vendor SDK.
package providertest

import (
	"errors"
	"testing"

	"github.com/mukezhz/code-mode/llm"

	// Both providers, so llm.New can select either.
	_ "github.com/mukezhz/code-mode/llm/claude"
	_ "github.com/mukezhz/code-mode/llm/openai"
)

func TestNewSelectsTheProviderFromTheEnvironment(t *testing.T) {
	for name, test := range map[string]struct {
		env  map[string]string
		want string
	}{
		"explicit claude":     {map[string]string{"LLM_PROVIDER": "claude", "ANTHROPIC_API_KEY": "test"}, "claude/claude-opus-5"},
		"explicit openai":     {map[string]string{"LLM_PROVIDER": "openai", "OPENAI_API_KEY": "test"}, "openai/gpt-5"},
		"anthropic alias":     {map[string]string{"LLM_PROVIDER": "Anthropic", "ANTHROPIC_API_KEY": "test"}, "claude/claude-opus-5"},
		"inferred from key":   {map[string]string{"OPENAI_API_KEY": "test"}, "openai/gpt-5"},
		"claude wins on both": {map[string]string{"ANTHROPIC_API_KEY": "test", "OPENAI_API_KEY": "test"}, "claude/claude-opus-5"},
		"model override":      {map[string]string{"LLM_PROVIDER": "claude", "ANTHROPIC_API_KEY": "test", "ANTHROPIC_MODEL": "claude-sonnet-5"}, "claude/claude-sonnet-5"},

		// A base URL stands in for credentials: local endpoints often do not
		// authenticate, and most of them speak the OpenAI protocol.
		"base URL alone":       {map[string]string{"LLM_BASE_URL": "http://localhost:11434/v1", "OPENAI_MODEL": "llama3"}, "openai/llama3"},
		"openai base URL":      {map[string]string{"OPENAI_BASE_URL": "http://localhost:8000/v1"}, "openai/gpt-5"},
		"anthropic base URL":   {map[string]string{"ANTHROPIC_BASE_URL": "http://localhost:8000"}, "claude/claude-opus-5"},
		"base URL with claude": {map[string]string{"LLM_PROVIDER": "claude", "LLM_BASE_URL": "http://localhost:8000"}, "claude/claude-opus-5"},
	} {
		t.Run(name, func(t *testing.T) {
			setEnv(t, test.env)
			client, err := llm.New()
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}
			if got := client.Name(); got != test.want {
				t.Fatalf("Name() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNewReportsMissingCredentials(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"nothing set":      {},
		"claude, no key":   {"LLM_PROVIDER": "claude"},
		"openai, no key":   {"LLM_PROVIDER": "openai"},
		"wrong key for it": {"LLM_PROVIDER": "claude", "OPENAI_API_KEY": "test"},
		"other base URL":   {"LLM_PROVIDER": "claude", "OPENAI_BASE_URL": "http://localhost:8000/v1"},
	} {
		t.Run(name, func(t *testing.T) {
			setEnv(t, env)
			if _, err := llm.New(); !errors.Is(err, llm.ErrNoProvider) {
				t.Fatalf("error = %v, want ErrNoProvider", err)
			}
		})
	}
}

func TestNewRejectsAnUnknownProvider(t *testing.T) {
	setEnv(t, map[string]string{"LLM_PROVIDER": "llama", "ANTHROPIC_API_KEY": "test"})
	_, err := llm.New()
	if err == nil {
		t.Fatal("New returned no error for an unknown provider")
	}
	if errors.Is(err, llm.ErrNoProvider) {
		t.Fatalf("error = %v, want an unsupported-provider error", err)
	}
}

// setEnv clears every variable New reads, then applies env. t.Setenv restores
// the previous values when the test ends.
func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, name := range []string{
		"LLM_PROVIDER", "LLM_BASE_URL",
		"ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "ANTHROPIC_BASE_URL",
		"OPENAI_API_KEY", "OPENAI_MODEL", "OPENAI_BASE_URL",
	} {
		t.Setenv(name, env[name])
	}
}
