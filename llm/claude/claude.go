// Package claude speaks the Anthropic API.
//
// Blank-import it to make LLM_PROVIDER=claude available:
//
//	import _ "github.com/mukezhz/code-mode/llm/claude"
//
// Anthropic publishes no embeddings API, so capability search always uses
// llm/openai's embedder, whichever provider writes the programs.
package claude

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/mukezhz/code-mode/llm"
)

func init() {
	llm.RegisterProvider(llm.Provider{
		Name:     llm.ProviderClaude,
		Aliases:  []string{"anthropic"},
		New:      func(options llm.Options) (llm.Client, error) { return New(Options{Options: options}) },
		FromEnv:  OptionsFromEnv,
		Priority: 1,
	})
}

// DefaultModel is used when neither Options.Model nor ANTHROPIC_MODEL is set.
const DefaultModel = "claude-opus-5"

// DefaultMaxTokens bounds thinking and response text together. Claude Opus 5
// thinks by default, so a budget sized only for the program would truncate it.
const DefaultMaxTokens = 16000

// Thinking selects the extended-thinking mode.
type Thinking int

const (
	// ThinkingDefault leaves the model's own default in place, which is
	// adaptive thinking on current models. Setting Temperature or TopP
	// overrides this to ThinkingOff, because Anthropic rejects sampling
	// parameters alongside thinking.
	ThinkingDefault Thinking = iota
	// ThinkingOff disables extended thinking. Programs are measurably worse
	// without it; choose this only when you need sampling control.
	ThinkingOff
	// ThinkingAdaptive asks the model to decide how much to think.
	ThinkingAdaptive
)

// Options configures the Claude client. The embedded llm.Options carries
// everything portable; the fields beside it are Anthropic-specific.
type Options struct {
	llm.Options

	// Thinking selects the extended-thinking mode. See Thinking.
	Thinking Thinking

	// Fallbacks lets a request be served by a recommended fallback model when
	// a safety classifier declines it. Nil enables it against api.anthropic.com
	// and disables it against a custom endpoint, which may not implement the
	// beta. Use llm.Bool-style explicitness only if you need to force it.
	Fallbacks *bool

	// Betas are additional beta headers to send.
	Betas []anthropic.AnthropicBeta

	// Request are raw SDK request options — retries, headers, an HTTP client.
	// The escape hatch for anything this struct does not model.
	Request []option.RequestOption
}

type client struct {
	client    anthropic.Client
	model     string
	maxTokens int64
	params    Options
	fallbacks bool
	thinking  anthropic.BetaThinkingConfigParamUnion
}

// New builds a Claude client from options. It reads no environment variable;
// Model and MaxTokens fall back to this package's defaults, and everything else
// is exactly what you passed.
//
// Use OptionsFromEnv to read ANTHROPIC_* from the environment first.
func New(options Options) (llm.Client, error) {
	resolved, err := options.Options.WithDefaults(llm.Defaults{
		Model:     DefaultModel,
		MaxTokens: DefaultMaxTokens,
	})
	if err != nil {
		return nil, err
	}
	options.Options = resolved

	if resolved.APIKey == "" && resolved.BaseURL == "" {
		return nil, fmt.Errorf("%w: set Options.APIKey or Options.BaseURL", llm.ErrNoProvider)
	}

	requestOptions := append([]option.RequestOption{option.WithAPIKey(resolved.APIKey)}, options.Request...)
	if resolved.BaseURL != "" {
		requestOptions = append(requestOptions, option.WithBaseURL(resolved.BaseURL))
	}
	if resolved.Timeout > 0 {
		requestOptions = append(requestOptions, option.WithRequestTimeout(resolved.Timeout))
	}

	// Fallbacks default off against a custom endpoint, which may not implement
	// the beta at all.
	fallbacks := resolved.BaseURL == ""
	if options.Fallbacks != nil {
		fallbacks = *options.Fallbacks
	}

	return &client{
		client:    anthropic.NewClient(requestOptions...),
		model:     resolved.Model,
		maxTokens: int64(resolved.MaxTokens),
		params:    options,
		fallbacks: fallbacks,
		thinking:  thinkingConfig(options),
	}, nil
}

// thinkingConfig resolves the thinking mode, honouring the rule that Anthropic
// rejects a temperature or top_p alongside extended thinking. Asking for
// sampling control is taken as asking for thinking to be off, because the
// alternative is a request that always fails.
func thinkingConfig(options Options) anthropic.BetaThinkingConfigParamUnion {
	mode := options.Thinking
	if mode == ThinkingDefault && options.Sampling() {
		mode = ThinkingOff
	}
	switch mode {
	case ThinkingOff:
		return anthropic.BetaThinkingConfigParamUnion{
			OfDisabled: &anthropic.BetaThinkingConfigDisabledParam{},
		}
	case ThinkingAdaptive:
		return anthropic.BetaThinkingConfigParamUnion{
			OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{},
		}
	default:
		return anthropic.BetaThinkingConfigParamUnion{}
	}
}

func (client *client) Name() string { return "claude/" + client.model }

func (client *client) Complete(ctx context.Context, system, user string) (string, error) {
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(client.model),
		MaxTokens: client.maxTokens,
		System: []anthropic.BetaTextBlockParam{{
			Text: system,
			// The capability declarations are stable across a session, so
			// caching them keeps repeated requests cheap.
			CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(user)),
		},
		Thinking: client.thinking,
	}
	if temperature := client.params.Temperature; temperature != nil {
		params.Temperature = anthropic.Float(*temperature)
	}
	if topP := client.params.TopP; topP != nil {
		params.TopP = anthropic.Float(*topP)
	}
	if len(client.params.StopSequences) > 0 {
		params.StopSequences = client.params.StopSequences
	}
	params.Betas = client.params.Betas
	if client.fallbacks {
		// A safety classifier may decline; the recommended fallback model
		// serves the request instead of the call failing.
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
		params.Betas = append(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}

	response, err := client.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	if response.StopReason == anthropic.BetaStopReasonRefusal {
		return "", errors.New("claude: request was declined")
	}

	var builder strings.Builder
	for _, block := range response.Content {
		if text, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			builder.WriteString(text.Text)
		}
	}
	if builder.Len() == 0 {
		return "", fmt.Errorf("claude: empty response (stop reason %q)", response.StopReason)
	}
	return builder.String(), nil
}
