// This file is the only place in the package that reads the process
// environment. Everything else takes an Options, so an application that
// configures itself some other way — flags, a config file, a secret mount —
// never has the environment consulted behind its back.
//
// The convention holds library-wide: a name containing FromEnv reads the
// environment, and nothing else does.

package llm

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment variables read by OptionsFromEnv. Provider-specific variables
// live with their provider package.
const (
	// EnvProvider selects the provider: claude or openai.
	EnvProvider = "LLM_PROVIDER"
	// EnvBaseURL applies to whichever provider is selected. A provider's own
	// base URL variable wins over it.
	EnvBaseURL = "LLM_BASE_URL"
	// EnvMaxTokens bounds thinking and response text together.
	EnvMaxTokens = "LLM_MAX_TOKENS"
	// EnvTemperature and EnvTopP leave the provider default alone when unset.
	EnvTemperature = "LLM_TEMPERATURE"
	EnvTopP        = "LLM_TOP_P"
	// EnvTimeout bounds one completion request, as a Go duration.
	EnvTimeout = "LLM_TIMEOUT"
)

// OptionsFromEnv reads configuration from the process environment and returns
// it as an Options. Nothing is applied for you: inspect it, override any field,
// and pass it to New.
//
//	options, err := llm.OptionsFromEnv()
//	options.Model = "claude-sonnet-5"   // your config wins
//	model, err := llm.New(options)
//
// LLM_PROVIDER selects the provider. When it is unset, the registered provider
// with the lowest Priority whose own variables are set is chosen, so a single
// API key is enough to get started. It returns ErrNoProvider when nothing is
// configured.
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

	// Unset means "whichever provider is configured", so a single variable is
	// enough to get started.
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

// NewFromEnv is OptionsFromEnv followed by New, for callers with no
// configuration of their own to layer on top.
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

// sharedFromEnv reads the LLM_* variables that apply to every provider.
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

// Env returns the trimmed value of name, or fallback when it is unset or
// blank. Provider packages use it in their own FromEnv.
func Env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// EnvInt parses name as an integer, returning zero when it is unset. An
// unparseable value is an error rather than a silent zero, because a typo in a
// deployment variable should fail at startup.
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

// EnvFloat parses name as a float, reporting whether it was set at all. The
// distinction matters: a temperature of 0 is a real setting.
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

// EnvDuration parses name as a Go duration, returning zero when it is unset.
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
