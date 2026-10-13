package openai

import (
	"os"
	"strings"

	"github.com/mukezhz/codemode/llm"
)

const (
	EnvAPIKey  = "OPENAI_API_KEY"
	EnvModel   = "OPENAI_MODEL"
	EnvBaseURL = "OPENAI_BASE_URL"

	EnvEmbeddingAPIKey  = "EMBEDDING_API_KEY"
	EnvEmbeddingModel   = "EMBEDDING_MODEL"
	EnvEmbeddingBaseURL = "EMBEDDING_BASE_URL"
)

// OptionsFromEnv reads OPENAI_* into an llm.Options and reports whether any were set; a bare LLM_BASE_URL also counts.
func OptionsFromEnv() (llm.Options, bool, error) {
	options := llm.Options{
		Provider: llm.ProviderOpenAI,
		APIKey:   strings.TrimSpace(os.Getenv(EnvAPIKey)),
		Model:    strings.TrimSpace(os.Getenv(EnvModel)),
		BaseURL:  strings.TrimSpace(os.Getenv(EnvBaseURL)),
	}
	configured := options.APIKey != "" || options.BaseURL != "" ||
		strings.TrimSpace(os.Getenv(llm.EnvBaseURL)) != ""
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

// EmbedOptionsFromEnv reads EMBEDDING_*, falling back to OPENAI_* and then LLM_BASE_URL.
func EmbedOptionsFromEnv() (EmbedOptions, error) {
	options := EmbedOptions{
		APIKey:  strings.TrimSpace(os.Getenv(EnvEmbeddingAPIKey)),
		Model:   strings.TrimSpace(os.Getenv(EnvEmbeddingModel)),
		BaseURL: strings.TrimSpace(os.Getenv(EnvEmbeddingBaseURL)),
	}
	if options.APIKey == "" {
		options.APIKey = strings.TrimSpace(os.Getenv(EnvAPIKey))
	}
	if options.BaseURL == "" {
		options.BaseURL = llm.Env(EnvBaseURL, strings.TrimSpace(os.Getenv(llm.EnvBaseURL)))
	}
	timeout, err := llm.EnvDuration(llm.EnvTimeout)
	if err != nil {
		return options, err
	}
	options.Timeout = timeout
	return options, nil
}

func NewEmbedderFromEnv(options EmbedOptions) (llm.Embedder, error) {
	fromEnv, err := EmbedOptionsFromEnv()
	if err != nil {
		return llm.Embedder{}, err
	}
	return NewEmbedder(options.Merge(fromEnv))
}
