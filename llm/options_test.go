package llm

import (
	"testing"
	"time"
)

var testDefaults = Defaults{
	Model:      "default-model",
	MaxTokens:  1000,
	ModelEnv:   "TEST_MODEL",
	APIKeyEnv:  "TEST_API_KEY",
	BaseURLEnv: "TEST_BASE_URL",
}

func clearOptionsEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"TEST_MODEL", "TEST_API_KEY", "TEST_BASE_URL", "LLM_BASE_URL",
		"LLM_MAX_TOKENS", "LLM_TEMPERATURE", "LLM_TOP_P", "LLM_TIMEOUT",
	} {
		t.Setenv(name, "")
	}
}

// Explicit beats provider variable beats shared variable beats default. Every
// provider calls Resolve, so this is the whole precedence contract.
func TestResolvePrefersExplicitOverEnvironment(t *testing.T) {
	clearOptionsEnv(t)
	t.Setenv("TEST_MODEL", "env-model")
	t.Setenv("LLM_MAX_TOKENS", "2000")
	t.Setenv("LLM_TEMPERATURE", "0.9")

	resolved, err := Options{
		Model:       "explicit-model",
		MaxTokens:   3000,
		Temperature: Float(0.1),
	}.Resolve(testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "explicit-model" {
		t.Errorf("Model = %q, want the explicit value", resolved.Model)
	}
	if resolved.MaxTokens != 3000 {
		t.Errorf("MaxTokens = %d, want the explicit value", resolved.MaxTokens)
	}
	if *resolved.Temperature != 0.1 {
		t.Errorf("Temperature = %v, want the explicit value", *resolved.Temperature)
	}
}

func TestResolveFallsBackThroughEnvironmentToDefaults(t *testing.T) {
	clearOptionsEnv(t)
	t.Setenv("TEST_MODEL", "env-model")
	t.Setenv("LLM_MAX_TOKENS", "2000")
	t.Setenv("LLM_TIMEOUT", "45s")

	resolved, err := Options{}.Resolve(testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "env-model" {
		t.Errorf("Model = %q, want the environment value", resolved.Model)
	}
	if resolved.MaxTokens != 2000 {
		t.Errorf("MaxTokens = %d, want the environment value", resolved.MaxTokens)
	}
	if resolved.Timeout != 45*time.Second {
		t.Errorf("Timeout = %v, want 45s", resolved.Timeout)
	}

	clearOptionsEnv(t)
	resolved, err = Options{}.Resolve(testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "default-model" || resolved.MaxTokens != 1000 {
		t.Errorf("resolved = %+v, want the package defaults", resolved)
	}
	if resolved.Temperature != nil {
		t.Errorf("Temperature = %v, want nil so the provider default stands", *resolved.Temperature)
	}
}

// Temperature 0 is a real setting, so unset has to be a distinct state.
func TestResolveDistinguishesZeroTemperatureFromUnset(t *testing.T) {
	clearOptionsEnv(t)
	resolved, err := Options{Temperature: Float(0)}.Resolve(testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Temperature == nil || *resolved.Temperature != 0 {
		t.Fatalf("Temperature = %v, want an explicit 0", resolved.Temperature)
	}
	if !resolved.Sampling() {
		t.Fatal("Sampling() = false for an explicit temperature of 0")
	}

	clearOptionsEnv(t)
	t.Setenv("LLM_TEMPERATURE", "0")
	resolved, err = Options{}.Resolve(testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Temperature == nil || *resolved.Temperature != 0 {
		t.Fatalf("Temperature = %v, want LLM_TEMPERATURE=0 to be honoured", resolved.Temperature)
	}
}

// A typo in a deployment variable should fail loudly at startup.
func TestResolveRejectsUnparseableEnvironmentValues(t *testing.T) {
	for name, value := range map[string]string{
		"LLM_MAX_TOKENS":  "lots",
		"LLM_TEMPERATURE": "warm",
		"LLM_TOP_P":       "most",
		"LLM_TIMEOUT":     "soon",
	} {
		t.Run(name, func(t *testing.T) {
			clearOptionsEnv(t)
			t.Setenv(name, value)
			if _, err := (Options{}).Resolve(testDefaults); err == nil {
				t.Fatalf("Resolve accepted %s=%q", name, value)
			}
		})
	}
}

func TestResolveFallsBackToTheSharedBaseURL(t *testing.T) {
	clearOptionsEnv(t)
	t.Setenv("LLM_BASE_URL", "http://shared")
	resolved, err := Options{}.Resolve(testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.BaseURL != "http://shared" {
		t.Fatalf("BaseURL = %q, want the shared fallback", resolved.BaseURL)
	}

	t.Setenv("TEST_BASE_URL", "http://specific")
	resolved, err = Options{}.Resolve(testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.BaseURL != "http://specific" {
		t.Fatalf("BaseURL = %q, want the provider value to win", resolved.BaseURL)
	}
}
