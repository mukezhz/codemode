// Package openai speaks the OpenAI API.
//
// Blank-import it to make LLM_PROVIDER=openai available:
//
//	import _ "github.com/mukezhz/codemode/llm/openai"
//
// Most self-hosted and third-party endpoints speak this protocol, so pointing
// OPENAI_BASE_URL at one reaches it without another provider. It also holds
// NewEmbedder, which capability search uses regardless of which provider
// writes the programs.
package openai

import (
	"context"
	"fmt"
	"strings"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"github.com/mukezhz/codemode/llm"
)

func init() {
	llm.RegisterProvider(llm.Provider{
		Name:     llm.ProviderOpenAI,
		New:      func(options llm.Options) (llm.Client, error) { return New(Options{Options: options}) },
		FromEnv:  OptionsFromEnv,
		Priority: 2,
	})
}

// DefaultModel is used when neither Options.Model nor OPENAI_MODEL is set. Set
// one of those to pin whichever model your account is entitled to.
const DefaultModel = "gpt-5"

// DefaultMaxTokens bounds reasoning and response text together.
const DefaultMaxTokens = 16000

// Options configures the OpenAI client. The embedded llm.Options carries
// everything portable; the fields beside it are OpenAI-specific.
type Options struct {
	llm.Options

	// ReasoningEffort tunes how much a reasoning model thinks before
	// answering: "minimal", "low", "medium", or "high". Empty leaves the
	// model's default. Ignored by endpoints that do not implement it.
	ReasoningEffort string

	// Seed asks for reproducible sampling where the endpoint supports it.
	// Nil leaves it unset.
	Seed *int64

	// Request are raw SDK request options — retries, headers, an HTTP client.
	// The escape hatch for anything this struct does not model.
	Request []option.RequestOption
}

type client struct {
	client openaisdk.Client
	model  string
	params Options
}

// New builds an OpenAI client from options. It reads no environment variable;
// Model and MaxTokens fall back to this package's defaults, and everything else
// is exactly what you passed.
//
// Use OptionsFromEnv to read OPENAI_* from the environment first.
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

	return &client{
		client: openaisdk.NewClient(requestOptions...),
		model:  resolved.Model,
		params: options,
	}, nil
}

func (client *client) Name() string { return "openai/" + client.model }

// request builds the completion parameters shared by Complete and
// CompleteStream, so the two cannot drift in what they ask the model for.
func (client *client) request(system, user string) openaisdk.ChatCompletionNewParams {
	params := openaisdk.ChatCompletionNewParams{
		Model:               client.model,
		MaxCompletionTokens: openaisdk.Int(int64(client.params.MaxTokens)),
		Messages: []openaisdk.ChatCompletionMessageParamUnion{
			openaisdk.SystemMessage(system),
			openaisdk.UserMessage(user),
		},
	}
	if temperature := client.params.Temperature; temperature != nil {
		params.Temperature = openaisdk.Float(*temperature)
	}
	if topP := client.params.TopP; topP != nil {
		params.TopP = openaisdk.Float(*topP)
	}
	if seed := client.params.Seed; seed != nil {
		params.Seed = openaisdk.Int(*seed)
	}
	if effort := strings.TrimSpace(client.params.ReasoningEffort); effort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(effort)
	}
	if stop := client.params.StopSequences; len(stop) > 0 {
		params.Stop = openaisdk.ChatCompletionNewParamsStopUnion{OfStringArray: stop}
	}

	return params
}

func (client *client) Complete(ctx context.Context, system, user string) (string, error) {
	params := client.request(system, user)
	response, err := client.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}
	if len(response.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices returned")
	}
	content := strings.TrimSpace(response.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("openai: empty response (finish reason %q)", response.Choices[0].FinishReason)
	}
	return content, nil
}

// CompleteStream implements llm.Streamer.
func (client *client) CompleteStream(ctx context.Context, system, user string, chunk func(string)) (string, error) {
	stream := client.client.Chat.Completions.NewStreaming(ctx, client.request(system, user))
	defer stream.Close()

	var builder strings.Builder
	for stream.Next() {
		event := stream.Current()
		if len(event.Choices) == 0 {
			continue
		}
		delta := event.Choices[0].Delta.Content
		if delta == "" {
			continue
		}
		builder.WriteString(delta)
		if chunk != nil {
			chunk(delta)
		}
	}
	if err := stream.Err(); err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}
	content := strings.TrimSpace(builder.String())
	if content == "" {
		return "", fmt.Errorf("openai: empty response")
	}
	return content, nil
}
