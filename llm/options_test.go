package llm

import (
	"testing"
	"time"
)

var testDefaults = Defaults{Model: "default-model", MaxTokens: 1000}

// WithDefaults must not consult the environment: an application that set a
// field has decided, and a deployment variable must not override it.
func TestWithDefaultsIgnoresTheEnvironment(t *testing.T) {
	t.Setenv("LLM_MAX_TOKENS", "9999")
	t.Setenv("LLM_TEMPERATURE", "0.9")

	resolved, err := Options{Model: "explicit", MaxTokens: 3000}.WithDefaults(testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "explicit" || resolved.MaxTokens != 3000 {
		t.Fatalf("resolved = %+v, want the explicit values", resolved)
	}
	if resolved.Temperature != nil {
		t.Fatalf("Temperature = %v, want nil despite LLM_TEMPERATURE", *resolved.Temperature)
	}
}

func TestWithDefaultsFillsOnlyZeroFields(t *testing.T) {
	resolved, err := Options{}.WithDefaults(testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "default-model" || resolved.MaxTokens != 1000 {
		t.Fatalf("resolved = %+v, want the package defaults", resolved)
	}
}

func TestWithDefaultsRejectsNegativeBounds(t *testing.T) {
	if _, err := (Options{MaxTokens: -1}).WithDefaults(testDefaults); err == nil {
		t.Error("WithDefaults accepted a negative MaxTokens")
	}
	if _, err := (Options{Timeout: -time.Second}).WithDefaults(testDefaults); err == nil {
		t.Error("WithDefaults accepted a negative Timeout")
	}
}

// Merge is how a caller layers their own configuration over a set read from the
// environment, so the receiver has to win every field it sets.
func TestMergeKeepsTheReceiversSetFields(t *testing.T) {
	mine := Options{Model: "mine", Temperature: Float(0)}
	theirs := Options{
		Provider: "claude", Model: "theirs", APIKey: "key", BaseURL: "url",
		MaxTokens: 500, Temperature: Float(0.9), TopP: Float(0.5),
		StopSequences: []string{"stop"}, Timeout: time.Minute,
	}

	merged := mine.Merge(theirs)
	if merged.Model != "mine" {
		t.Errorf("Model = %q, want the receiver's", merged.Model)
	}
	if *merged.Temperature != 0 {
		t.Errorf("Temperature = %v, want the receiver's explicit 0", *merged.Temperature)
	}
	// Everything the receiver left zero comes from the other side.
	if merged.Provider != "claude" || merged.APIKey != "key" || merged.BaseURL != "url" {
		t.Errorf("merged = %+v, want the unset fields filled", merged)
	}
	if merged.MaxTokens != 500 || *merged.TopP != 0.5 || merged.Timeout != time.Minute {
		t.Errorf("merged = %+v, want the unset fields filled", merged)
	}
	if len(merged.StopSequences) != 1 {
		t.Errorf("StopSequences = %v, want the other side's", merged.StopSequences)
	}
}

// Temperature 0 is a real setting, so unset has to be a distinct state.
func TestSamplingDistinguishesZeroFromUnset(t *testing.T) {
	if (Options{}).Sampling() {
		t.Error("Sampling() = true for a zero Options")
	}
	if !(Options{Temperature: Float(0)}).Sampling() {
		t.Error("Sampling() = false for an explicit temperature of 0")
	}
}
