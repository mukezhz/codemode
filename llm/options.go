package llm

import (
	"errors"
	"time"
)

// Options configures a client; fields a provider does not support are ignored.
type Options struct {
	// Provider names the provider; required by New.
	Provider string

	// Model is the model name; empty applies the provider's DefaultModel.
	Model string

	// APIKey authenticates the request; may be empty when BaseURL is set.
	APIKey string

	// BaseURL overrides the vendor endpoint; empty uses the vendor's own.
	BaseURL string

	// MaxTokens bounds the response including thinking; zero applies DefaultMaxTokens.
	MaxTokens int

	// Temperature controls sampling; nil keeps the provider default. Setting it
	// on Claude disables extended thinking.
	Temperature *float64

	// TopP is nucleus sampling; nil keeps the provider default.
	TopP *float64

	// StopSequences end generation early when produced.
	StopSequences []string

	// Timeout bounds a single completion request; zero keeps the SDK default.
	Timeout time.Duration
}

// Float returns a pointer to value, for setting Temperature and TopP.
func Float(value float64) *float64 { return &value }

// Defaults are a provider package's fallbacks for the two fields that have one.
type Defaults struct {
	Model     string
	MaxTokens int
}

// WithDefaults fills Model and MaxTokens when they are zero and validates the rest.
func (options Options) WithDefaults(defaults Defaults) (Options, error) {
	if options.Model == "" {
		options.Model = defaults.Model
	}
	if options.MaxTokens == 0 {
		options.MaxTokens = defaults.MaxTokens
	}
	if options.MaxTokens < 0 {
		return options, errors.New("max tokens must not be negative")
	}
	if options.Timeout < 0 {
		return options, errors.New("timeout must not be negative")
	}
	return options, nil
}

// Sampling reports whether any sampling parameter was set.
func (options Options) Sampling() bool {
	return options.Temperature != nil || options.TopP != nil
}

// Merge fills every zero field of options from other.
func (options Options) Merge(other Options) Options {
	if options.Provider == "" {
		options.Provider = other.Provider
	}
	if options.Model == "" {
		options.Model = other.Model
	}
	if options.APIKey == "" {
		options.APIKey = other.APIKey
	}
	if options.BaseURL == "" {
		options.BaseURL = other.BaseURL
	}
	if options.MaxTokens == 0 {
		options.MaxTokens = other.MaxTokens
	}
	if options.Temperature == nil {
		options.Temperature = other.Temperature
	}
	if options.TopP == nil {
		options.TopP = other.TopP
	}
	if len(options.StopSequences) == 0 {
		options.StopSequences = other.StopSequences
	}
	if options.Timeout == 0 {
		options.Timeout = other.Timeout
	}
	return options
}
