package claude

import (
	"os"
	"strings"

	"github.com/mukezhz/codemode/llm"
)

const (
	EnvAPIKey  = "ANTHROPIC_API_KEY"
	EnvModel   = "ANTHROPIC_MODEL"
	EnvBaseURL = "ANTHROPIC_BASE_URL"
)

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

func NewFromEnv(options Options) (llm.Client, error) {
	fromEnv, _, err := OptionsFromEnv()
	if err != nil {
		return nil, err
	}
	options.Options = options.Options.Merge(fromEnv)
	return New(options)
}
