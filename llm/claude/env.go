package claude

import (
	"os"
	"strings"

	"github.com/mukezhz/codemode/llm"
)

// Environment variables read by OptionsFromEnv.
const (
	EnvAPIKey  = "ANTHROPIC_API_KEY"
	EnvModel   = "ANTHROPIC_MODEL"
	EnvBaseURL = "ANTHROPIC_BASE_URL"
)

// OptionsFromEnv reads ANTHROPIC_* into an llm.Options and reports whether any were set.
func OptionsFromEnv() (llm.Options, bool, error) {
	options := llm.Options{
		Provider: llm.ProviderClaude,
		APIKey:   strings.TrimSpace(os.Getenv(EnvAPIKey)),
		Model:    strings.TrimSpace(os.Getenv(EnvModel)),
		BaseURL:  strings.TrimSpace(os.Getenv(EnvBaseURL)),
	}
	// LLM_BASE_URL names no provider, so it must not mark this one configured.
	configured := options.APIKey != "" || options.BaseURL != ""
	return options, configured, nil
}

// NewFromEnv is OptionsFromEnv followed by New.
func NewFromEnv(options Options) (llm.Client, error) {
	fromEnv, _, err := OptionsFromEnv()
	if err != nil {
		return nil, err
	}
	options.Options = options.Options.Merge(fromEnv)
	return New(options)
}
