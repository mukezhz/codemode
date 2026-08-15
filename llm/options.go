package llm

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Options configures a client. Every field is optional: a zero Options is
// entirely environment-driven, and any field you set wins over the environment.
// That order is the point — deployment configuration should not silently
// override a decision the application made deliberately.
//
//	model, err := llm.New(llm.Options{
//		Model:       "claude-sonnet-5",
//		Temperature: llm.Float(0.2),
//		MaxTokens:   8000,
//	})
//
// Fields a provider does not support are ignored rather than rejected, so the
// same Options can be handed to whichever provider the environment selects.
type Options struct {
	// Provider names the provider, overriding LLM_PROVIDER. Ignored when
	// calling a provider package's own New.
	Provider string

	// Model is the model name, such as "claude-opus-5" or "gpt-5". Falls back
	// to ANTHROPIC_MODEL / OPENAI_MODEL, then to the provider's default.
	Model string

	// APIKey authenticates the request. Falls back to ANTHROPIC_API_KEY /
	// OPENAI_API_KEY. Empty is allowed when BaseURL points at an endpoint that
	// does not authenticate.
	APIKey string

	// BaseURL reaches an alternative endpoint speaking the provider's
	// protocol. Falls back to ANTHROPIC_BASE_URL / OPENAI_BASE_URL, then
	// LLM_BASE_URL.
	BaseURL string

	// MaxTokens bounds the response, including any reasoning or thinking
	// tokens. Zero falls back to LLM_MAX_TOKENS, then to the provider's
	// default, which is sized for a program plus the thinking that precedes
	// it.
	MaxTokens int

	// Temperature controls sampling. Nil leaves the provider's default in
	// place; use Float to set it, so that 0 is distinguishable from unset.
	// Falls back to LLM_TEMPERATURE.
	//
	// Setting it on Claude turns extended thinking off, because Anthropic
	// rejects a temperature alongside thinking. Programs are better with
	// thinking than with a low temperature, so prefer leaving this nil.
	Temperature *float64

	// TopP is nucleus sampling. Nil leaves the provider's default. Falls back
	// to LLM_TOP_P. The same thinking caveat as Temperature applies.
	TopP *float64

	// StopSequences end generation early when produced. Rarely useful here:
	// the whole response is one program.
	StopSequences []string

	// Timeout bounds a single completion request. Zero falls back to
	// LLM_TIMEOUT, then leaves the SDK's own default in place. It is separate
	// from the ctx deadline, which bounds the whole agent run.
	Timeout time.Duration
}

// Float returns a pointer to value, for setting Temperature and TopP.
func Float(value float64) *float64 { return &value }

// Defaults are the provider-specific fallbacks Resolve applies. Provider
// packages fill this in; callers never see it.
type Defaults struct {
	// Model is used when neither Options.Model nor ModelEnv is set.
	Model string
	// MaxTokens is used when neither Options.MaxTokens nor LLM_MAX_TOKENS is
	// set.
	MaxTokens int
	// ModelEnv, APIKeyEnv, and BaseURLEnv name this provider's own variables,
	// such as ANTHROPIC_MODEL.
	ModelEnv, APIKeyEnv, BaseURLEnv string
}

// Resolve fills every zero field from the environment and then from defaults,
// leaving set fields alone. Provider packages call it once in New, so the
// precedence rule — explicit, then provider variable, then shared variable,
// then default — is identical everywhere.
func (options Options) Resolve(defaults Defaults) (Options, error) {
	if options.Model == "" {
		options.Model = Env(defaults.ModelEnv, defaults.Model)
	}
	if options.APIKey == "" {
		options.APIKey = strings.TrimSpace(os.Getenv(defaults.APIKeyEnv))
	}
	if options.BaseURL == "" {
		options.BaseURL = BaseURL(defaults.BaseURLEnv)
	}

	if options.MaxTokens == 0 {
		maxTokens, err := envInt("LLM_MAX_TOKENS")
		if err != nil {
			return options, err
		}
		if maxTokens == 0 {
			maxTokens = defaults.MaxTokens
		}
		options.MaxTokens = maxTokens
	}
	if options.MaxTokens < 0 {
		return options, fmt.Errorf("max tokens must not be negative")
	}

	if options.Temperature == nil {
		value, set, err := envFloat("LLM_TEMPERATURE")
		if err != nil {
			return options, err
		}
		if set {
			options.Temperature = &value
		}
	}
	if options.TopP == nil {
		value, set, err := envFloat("LLM_TOP_P")
		if err != nil {
			return options, err
		}
		if set {
			options.TopP = &value
		}
	}

	if options.Timeout == 0 {
		if value := strings.TrimSpace(os.Getenv("LLM_TIMEOUT")); value != "" {
			timeout, err := time.ParseDuration(value)
			if err != nil {
				return options, fmt.Errorf("LLM_TIMEOUT: %w", err)
			}
			options.Timeout = timeout
		}
	}
	if options.Timeout < 0 {
		return options, fmt.Errorf("timeout must not be negative")
	}

	return options, nil
}

// Sampling reports whether any sampling parameter was set. Providers use it
// where sampling and reasoning are mutually exclusive.
func (options Options) Sampling() bool {
	return options.Temperature != nil || options.TopP != nil
}

func envInt(name string) (int, error) {
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

func envFloat(name string) (float64, bool, error) {
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
