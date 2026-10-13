package llm

import (
	"errors"
	"time"
)

type Options struct {
	Provider string

	Model string

	// APIKey may be empty when BaseURL is set.
	APIKey string

	BaseURL string

	MaxTokens int

	// Setting Temperature on Claude disables extended thinking.
	Temperature *float64

	TopP *float64

	StopSequences []string

	Timeout time.Duration
}

func Float(value float64) *float64 { return &value }

type Defaults struct {
	Model     string
	MaxTokens int
}

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

func (options Options) Sampling() bool {
	return options.Temperature != nil || options.TopP != nil
}

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
