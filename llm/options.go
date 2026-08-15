package llm

import (
	"errors"
	"time"
)

// Options configures a client.
//
// This is the library's configuration surface. Nothing here is read from the
// process environment — your application already has a way to configure itself,
// and a library that reaches into os.Getenv fights it. If you want the
// environment consulted, ask for it explicitly with OptionsFromEnv, which
// returns a value you can inspect, log, and override before use.
//
//	model, err := llm.New(llm.Options{
//		Provider:    llm.ProviderClaude,
//		APIKey:      config.AnthropicKey,
//		Model:       "claude-sonnet-5",
//		Temperature: llm.Float(0.2),
//	})
//
// Fields a provider does not support are ignored rather than rejected, so the
// same Options can be handed to whichever provider you select.
type Options struct {
	// Provider names the provider: "claude" or "openai". Required by New;
	// ignored when calling a provider package's own New.
	Provider string

	// Model is the model name, such as "claude-opus-5" or "gpt-5". Empty
	// applies the provider package's DefaultModel.
	Model string

	// APIKey authenticates the request. Empty is allowed when BaseURL points
	// at an endpoint that does not authenticate.
	APIKey string

	// BaseURL reaches an alternative endpoint speaking the provider's
	// protocol. Empty uses the vendor's own.
	BaseURL string

	// MaxTokens bounds the response, including any reasoning or thinking
	// tokens. Zero applies the provider package's DefaultMaxTokens, which is
	// sized for a program plus the thinking that precedes it.
	MaxTokens int

	// Temperature controls sampling. Nil leaves the provider's default in
	// place; use Float to set it, so that 0 is distinguishable from unset.
	//
	// Setting it on Claude turns extended thinking off, because Anthropic
	// rejects a temperature alongside thinking. Programs are better with
	// thinking than with a low temperature, so prefer leaving this nil.
	Temperature *float64

	// TopP is nucleus sampling. Nil leaves the provider's default. The same
	// thinking caveat as Temperature applies.
	TopP *float64

	// StopSequences end generation early when produced. Rarely useful here:
	// the whole response is one program.
	StopSequences []string

	// Timeout bounds a single completion request. Zero leaves the SDK's own
	// default in place. It is separate from the ctx deadline, which bounds the
	// whole agent run.
	Timeout time.Duration
}

// Float returns a pointer to value, for setting Temperature and TopP.
func Float(value float64) *float64 { return &value }

// Defaults are a provider package's fallbacks for the two fields that have one.
type Defaults struct {
	Model     string
	MaxTokens int
}

// WithDefaults fills Model and MaxTokens when they are zero and validates the
// rest. Provider packages call it once in New, so every provider applies its
// defaults and its validation the same way.
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

// Sampling reports whether any sampling parameter was set. Providers use it
// where sampling and reasoning are mutually exclusive.
func (options Options) Sampling() bool {
	return options.Temperature != nil || options.TopP != nil
}

// Merge fills every zero field of options from other, so a caller can layer
// their own configuration over a set read from the environment.
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
