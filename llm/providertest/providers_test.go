// Package providertest holds cross-provider tests outside llm so llm links no vendor SDK.
package providertest

import (
	"errors"
	"testing"

	"github.com/mukezhz/codemode/llm"

	_ "github.com/mukezhz/codemode/llm/claude"
	_ "github.com/mukezhz/codemode/llm/openai"
)

func TestOptionsFromEnvSelectsTheProvider(t *testing.T) {
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

		"base URL alone":       {map[string]string{"LLM_BASE_URL": "http://localhost:11434/v1", "OPENAI_MODEL": "llama3"}, "openai/llama3"},
		"openai base URL":      {map[string]string{"OPENAI_BASE_URL": "http://localhost:8000/v1"}, "openai/gpt-5"},
		"anthropic base URL":   {map[string]string{"ANTHROPIC_BASE_URL": "http://localhost:8000"}, "claude/claude-opus-5"},
		"base URL with claude": {map[string]string{"LLM_PROVIDER": "claude", "LLM_BASE_URL": "http://localhost:8000"}, "claude/claude-opus-5"},
	} {
		t.Run(name, func(t *testing.T) {
			setEnv(t, test.env)
			client, err := llm.NewFromEnv()
			if err != nil {
				t.Fatalf("NewFromEnv returned error: %v", err)
			}
			if got := client.Name(); got != test.want {
				t.Fatalf("Name() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestOptionsFromEnvReportsMissingCredentials(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"nothing set":      {},
		"claude, no key":   {"LLM_PROVIDER": "claude"},
		"openai, no key":   {"LLM_PROVIDER": "openai"},
		"wrong key for it": {"LLM_PROVIDER": "claude", "OPENAI_API_KEY": "test"},
		"other base URL":   {"LLM_PROVIDER": "claude", "OPENAI_BASE_URL": "http://localhost:8000/v1"},
	} {
		t.Run(name, func(t *testing.T) {
			setEnv(t, env)
			if _, err := llm.NewFromEnv(); !errors.Is(err, llm.ErrNoProvider) {
				t.Fatalf("error = %v, want ErrNoProvider", err)
			}
		})
	}
}

func TestCallerConfigurationWinsOverTheEnvironment(t *testing.T) {
	setEnv(t, map[string]string{
		"ANTHROPIC_API_KEY": "test",
		"ANTHROPIC_MODEL":   "claude-sonnet-5",
		"LLM_MAX_TOKENS":    "1234",
	})

	options, err := llm.OptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if options.Model != "claude-sonnet-5" || options.MaxTokens != 1234 {
		t.Fatalf("options = %+v, want the environment values", options)
	}

	options.Model = "claude-opus-4-8"
	client, err := llm.New(options)
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "claude/claude-opus-4-8" {
		t.Fatalf("Name() = %q, want the override", client.Name())
	}
}

func TestNewIgnoresTheEnvironmentEntirely(t *testing.T) {
	setEnv(t, map[string]string{
		"LLM_PROVIDER":      "claude",
		"ANTHROPIC_API_KEY": "test",
		"ANTHROPIC_MODEL":   "claude-sonnet-5",
	})

	if _, err := llm.New(llm.Options{}); !errors.Is(err, llm.ErrNoProvider) {
		t.Fatalf("error = %v, want ErrNoProvider — New read LLM_PROVIDER", err)
	}
	client, err := llm.New(llm.Options{Provider: llm.ProviderClaude, APIKey: "explicit"})
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "claude/claude-opus-5" {
		t.Fatalf("Name() = %q, want the package default rather than ANTHROPIC_MODEL", client.Name())
	}
}

func TestNewRejectsAnUnknownProvider(t *testing.T) {
	setEnv(t, nil)
	_, err := llm.New(llm.Options{Provider: "llama", APIKey: "test"})
	if err == nil {
		t.Fatal("New returned no error for an unknown provider")
	}
	if errors.Is(err, llm.ErrNoProvider) {
		t.Fatalf("error = %v, want an unknown-provider error", err)
	}
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, name := range []string{
		"LLM_PROVIDER", "LLM_BASE_URL", "LLM_MAX_TOKENS",
		"LLM_TEMPERATURE", "LLM_TOP_P", "LLM_TIMEOUT",
		"ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "ANTHROPIC_BASE_URL",
		"OPENAI_API_KEY", "OPENAI_MODEL", "OPENAI_BASE_URL",
	} {
		t.Setenv(name, env[name])
	}
}
