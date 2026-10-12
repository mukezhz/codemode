package llm

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvProvider = "LLM_PROVIDER"
	// A provider's own base URL variable wins over EnvBaseURL.
	EnvBaseURL     = "LLM_BASE_URL"
	EnvMaxTokens   = "LLM_MAX_TOKENS"
	EnvTemperature = "LLM_TEMPERATURE"
	EnvTopP        = "LLM_TOP_P"
	EnvTimeout     = "LLM_TIMEOUT"
)

// Without LLM_PROVIDER, OptionsFromEnv picks the lowest-Priority configured provider, else ErrNoProvider.
func OptionsFromEnv() (Options, error) {
	shared, err := sharedFromEnv()
	if err != nil {
		return Options{}, err
	}

	if name := strings.ToLower(strings.TrimSpace(os.Getenv(EnvProvider))); name != "" {
		provider, found := lookup(name)
		if !found {
			return Options{}, unregisteredProvider(name)
		}
		options, err := providerFromEnv(provider)
		if err != nil {
			return Options{}, err
		}
		options.Provider = provider.Name
		return options.Merge(shared), nil
	}

	for _, provider := range byPriority() {
		if provider.FromEnv == nil {
			continue
		}
		options, configured, err := provider.FromEnv()
		if err != nil {
			return Options{}, err
		}
		if configured {
			options.Provider = provider.Name
			return options.Merge(shared), nil
		}
	}

	if len(RegisteredProviders()) == 0 {
		return Options{}, fmt.Errorf("%w: no providers are registered, blank-import %s or %s",
			ErrNoProvider, shipped[ProviderClaude], shipped[ProviderOpenAI])
	}
	return Options{}, fmt.Errorf("%w: set %s, ANTHROPIC_API_KEY, OPENAI_API_KEY, or %s",
		ErrNoProvider, EnvProvider, EnvBaseURL)
}

func NewFromEnv() (Client, error) {
	options, err := OptionsFromEnv()
	if err != nil {
		return nil, err
	}
	return New(options)
}

func providerFromEnv(provider Provider) (Options, error) {
	if provider.FromEnv == nil {
		return Options{}, nil
	}
	options, _, err := provider.FromEnv()
	return options, err
}

func sharedFromEnv() (Options, error) {
	options := Options{BaseURL: strings.TrimSpace(os.Getenv(EnvBaseURL))}

	maxTokens, err := EnvInt(EnvMaxTokens)
	if err != nil {
		return options, err
	}
	options.MaxTokens = maxTokens

	if value, set, err := EnvFloat(EnvTemperature); err != nil {
		return options, err
	} else if set {
		options.Temperature = &value
	}
	if value, set, err := EnvFloat(EnvTopP); err != nil {
		return options, err
	} else if set {
		options.TopP = &value
	}

	timeout, err := EnvDuration(EnvTimeout)
	if err != nil {
		return options, err
	}
	options.Timeout = timeout

	return options, nil
}

func Env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func EnvInt(name string) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func EnvFloat(name string) (float64, bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0, false, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, true, nil
}

func EnvDuration(name string) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}
