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

func TestNewAppliesDefaultsAndOverrides(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_BASE_URL", "LLM_BASE_URL", "LLM_MAX_TOKENS"} {
		t.Setenv(name, "")
	}
	t.Setenv("ANTHROPIC_API_KEY", "test")

	client, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "claude/"+DefaultModel {
		t.Fatalf("Name() = %q, want the package default model", client.Name())
	}

	t.Setenv("ANTHROPIC_MODEL", "claude-sonnet-5")
	client, err = New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "claude/claude-sonnet-5" {
		t.Fatalf("Name() = %q, want the environment model", client.Name())
	}

	client, err = New(Options{Options: llm.Options{Model: "claude-opus-4-8"}})
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "claude/claude-opus-4-8" {
		t.Fatalf("Name() = %q, want the explicit model to win", client.Name())
	}
}

// Fallbacks are an api.anthropic.com beta; a custom endpoint may not implement
// them, so they default off there and stay overridable.
func TestFallbacksDefaultOffAgainstACustomEndpoint(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("LLM_BASE_URL", "")

	direct, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !direct.(*client).fallbacks {
		t.Fatal("fallbacks are off against api.anthropic.com")
	}

	custom, err := New(Options{Options: llm.Options{BaseURL: "http://localhost:8000"}})
	if err != nil {
		t.Fatal(err)
	}
	if custom.(*client).fallbacks {
		t.Fatal("fallbacks are on against a custom endpoint")
	}

	forced := true
	overridden, err := New(Options{
		Options:   llm.Options{BaseURL: "http://localhost:8000"},
		Fallbacks: &forced,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !overridden.(*client).fallbacks {
		t.Fatal("an explicit Fallbacks was ignored")
	}
}
