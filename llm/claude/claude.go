// Package claude implements llm.Client for the Anthropic API.
package claude

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/mukezhz/codemode/llm"
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

// DefaultMaxTokens bounds thinking and response text together.
const DefaultMaxTokens = 16000

// Thinking selects the extended-thinking mode.
type Thinking int

const (
	// ThinkingDefault keeps the model default, or ThinkingOff when Temperature or TopP is set.
	ThinkingDefault Thinking = iota
	// ThinkingOff disables extended thinking.
	ThinkingOff
	// ThinkingAdaptive asks the model to decide how much to think.
	ThinkingAdaptive
)

// Options configures the Claude client with Anthropic-specific fields.
type Options struct {
	llm.Options

	// Thinking selects the extended-thinking mode.
	Thinking Thinking

	// Fallbacks enables server-side fallback models; nil enables it only
	// without a custom BaseURL.
	Fallbacks *bool

	// Betas are additional beta headers to send.
	Betas []anthropic.AnthropicBeta

	// Request are raw SDK request options.
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

// New builds a Claude client from options without reading the environment.
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

	// A custom endpoint may not implement the fallback beta.
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

// thinkingConfig resolves the thinking mode; Anthropic rejects sampling params alongside thinking.
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

// request builds the message parameters shared by Complete and CompleteStream.
func (client *client) request(system, user string) anthropic.BetaMessageNewParams {
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(client.model),
		MaxTokens: client.maxTokens,
		System: []anthropic.BetaTextBlockParam{{
			Text: system,
			// Declarations are stable across a session, so cache them.
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
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
		params.Betas = append(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}

	return params
}

func (client *client) Complete(ctx context.Context, system, user string) (string, error) {
	params := client.request(system, user)
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

// CompleteStream implements llm.Streamer.
func (client *client) CompleteStream(ctx context.Context, system, user string, chunk func(string)) (string, error) {
	stream := client.client.Beta.Messages.NewStreaming(ctx, client.request(system, user))
	defer stream.Close()

	var message anthropic.BetaMessage
	var builder strings.Builder
	for stream.Next() {
		event := stream.Current()
		if err := message.Accumulate(event); err != nil {
			return "", fmt.Errorf("claude: %w", err)
		}
		delta, ok := event.AsAny().(anthropic.BetaRawContentBlockDeltaEvent)
		if !ok {
			continue
		}
		text, ok := delta.Delta.AsAny().(anthropic.BetaTextDelta)
		if !ok || text.Text == "" {
			continue
		}
		builder.WriteString(text.Text)
		if chunk != nil {
			chunk(text.Text)
		}
	}
	if err := stream.Err(); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	if message.StopReason == anthropic.BetaStopReasonRefusal {
		return "", errors.New("claude: request was declined")
	}
	if builder.Len() == 0 {
		return "", fmt.Errorf("claude: empty response (stop reason %q)", message.StopReason)
	}
	return builder.String(), nil
}
