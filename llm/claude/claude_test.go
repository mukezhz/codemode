package claude

import (
	"testing"

	"github.com/mukezhz/codemode/llm"
)

func TestSamplingParametersDisableThinking(t *testing.T) {
	for name, options := range map[string]Options{
		"temperature": {Options: llm.Options{Temperature: llm.Float(0.2)}},
		"top p":       {Options: llm.Options{TopP: llm.Float(0.9)}},
	} {
		t.Run(name, func(t *testing.T) {
			if config := thinkingConfig(options); config.OfDisabled == nil {
				t.Fatalf("thinking = %+v, want it disabled alongside sampling", config)
			}
		})
	}
}

func TestThinkingDefaultsToTheModelsOwnBehaviour(t *testing.T) {
	config := thinkingConfig(Options{})
	if config.OfDisabled != nil || config.OfAdaptive != nil || config.OfEnabled != nil {
		t.Fatalf("thinking = %+v, want it left unset", config)
	}
}

func TestExplicitThinkingWinsOverInference(t *testing.T) {
	config := thinkingConfig(Options{
		Options:  llm.Options{Temperature: llm.Float(0.2)},
		Thinking: ThinkingAdaptive,
	})
	if config.OfAdaptive == nil {
		t.Fatalf("thinking = %+v, want the explicit adaptive choice", config)
	}
}

func TestNewIgnoresTheEnvironment(t *testing.T) {
	t.Setenv(EnvModel, "claude-from-env")
	t.Setenv(EnvAPIKey, "key-from-env")

	if _, err := New(Options{}); err == nil {
		t.Fatal("New succeeded with no APIKey — it read ANTHROPIC_API_KEY")
	}

	client, err := New(Options{Options: llm.Options{APIKey: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "claude/"+DefaultModel {
		t.Fatalf("Name() = %q, want the package default, not the environment", client.Name())
	}

	client, err = New(Options{Options: llm.Options{APIKey: "test", Model: "claude-opus-4-8"}})
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "claude/claude-opus-4-8" {
		t.Fatalf("Name() = %q, want the explicit model", client.Name())
	}
}

func TestOptionsFromEnvReadsAnthropicVariables(t *testing.T) {
	t.Setenv(EnvAPIKey, "key-from-env")
	t.Setenv(EnvModel, "claude-sonnet-5")
	t.Setenv(EnvBaseURL, "")

	options, configured, err := OptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !configured {
		t.Fatal("configured = false with ANTHROPIC_API_KEY set")
	}
	if options.APIKey != "key-from-env" || options.Model != "claude-sonnet-5" {
		t.Fatalf("options = %+v", options)
	}

	client, err := NewFromEnv(Options{Options: llm.Options{Model: "mine"}})
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "claude/mine" {
		t.Fatalf("Name() = %q, want the caller's model to win over the environment", client.Name())
	}
}

func TestOptionsFromEnvIgnoresTheSharedBaseURL(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(llm.EnvBaseURL, "http://localhost:11434/v1")

	if _, configured, err := OptionsFromEnv(); err != nil {
		t.Fatal(err)
	} else if configured {
		t.Fatal("configured = true from LLM_BASE_URL alone")
	}
}

func TestFallbacksDefaultOffAgainstACustomEndpoint(t *testing.T) {
	direct, err := New(Options{Options: llm.Options{APIKey: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if !direct.(*client).fallbacks {
		t.Fatal("fallbacks are off against api.anthropic.com")
	}

	custom, err := New(Options{Options: llm.Options{APIKey: "test", BaseURL: "http://localhost:8000"}})
	if err != nil {
		t.Fatal(err)
	}
	if custom.(*client).fallbacks {
		t.Fatal("fallbacks are on against a custom endpoint")
	}

	forced := true
	overridden, err := New(Options{
		Options:   llm.Options{APIKey: "test", BaseURL: "http://localhost:8000"},
		Fallbacks: &forced,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !overridden.(*client).fallbacks {
		t.Fatal("an explicit Fallbacks was ignored")
	}
}
