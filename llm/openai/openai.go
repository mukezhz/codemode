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

const DefaultModel = "gpt-5"

const DefaultMaxTokens = 16000

type Options struct {
	llm.Options

	ReasoningEffort string

	Seed *int64

	Request []option.RequestOption
}

type client struct {
	client openaisdk.Client
	model  string
	params Options
}

var (
	_ llm.Client   = (*client)(nil)
	_ llm.Streamer = (*client)(nil)
)

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
	llm.Report(ctx, llm.Usage{Input: response.Usage.PromptTokens, Output: response.Usage.CompletionTokens})
	if len(response.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices returned")
	}
	content := strings.TrimSpace(response.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("openai: empty response (finish reason %q)", response.Choices[0].FinishReason)
	}
	return content, nil
}

func (client *client) CompleteStream(ctx context.Context, system, user string, chunk func(string)) (string, error) {
	params := client.request(system, user)
	params.StreamOptions = openaisdk.ChatCompletionStreamOptionsParam{IncludeUsage: openaisdk.Bool(true)}
	stream := client.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var builder strings.Builder
	for stream.Next() {
		event := stream.Current()
		// Usage arrives on a final chunk with no choices.
		if event.Usage.TotalTokens > 0 {
			llm.Report(ctx, llm.Usage{Input: event.Usage.PromptTokens, Output: event.Usage.CompletionTokens})
		}
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
