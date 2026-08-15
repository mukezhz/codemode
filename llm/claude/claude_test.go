package claude

import (
	"testing"

	"github.com/mukezhz/code-mode/llm"
)

// Anthropic rejects a temperature alongside extended thinking, so asking for
// sampling control has to turn thinking off. Getting this wrong makes every
// request fail with a 400 that names neither cause.
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

// Without sampling parameters the model's own default must stand, so an
// unconfigured client still gets adaptive thinking.
func TestThinkingDefaultsToTheModelsOwnBehaviour(t *testing.T) {
	config := thinkingConfig(Options{})
	if config.OfDisabled != nil || config.OfAdaptive != nil || config.OfEnabled != nil {
		t.Fatalf("thinking = %+v, want it left unset", config)
	}
}

// An explicit choice wins, even against sampling parameters: the caller has
// said what they want and the API error is theirs to see.
func TestExplicitThinkingWinsOverInference(t *testing.T) {
	config := thinkingConfig(Options{
		Options:  llm.Options{Temperature: llm.Float(0.2)},
		Thinking: ThinkingAdaptive,
	})
	if config.OfAdaptive == nil {
		t.Fatalf("thinking = %+v, want the explicit adaptive choice", config)
	}
}

// New must ignore the environment entirely: a caller who passed a Model has
// decided, and one who passed none gets the package default, never whatever
// ANTHROPIC_MODEL happens to be.
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

// OptionsFromEnv is where ANTHROPIC_* is read, and the caller still wins.
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

// LLM_BASE_URL names no provider, so it must not make Claude look configured.
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

// Fallbacks are an api.anthropic.com beta; a custom endpoint may not implement
// them, so they default off there and stay overridable.
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
