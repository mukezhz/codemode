package llm

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// defaultOpenAIModel is used when OPENAI_MODEL is unset. Set that variable to
// pin whichever model your account is entitled to.
const defaultOpenAIModel = "gpt-5"

// openAIMaxTokens bounds reasoning and response text together.
const openAIMaxTokens = 16000

type openAI struct {
	client openai.Client
	model  string
}

func newOpenAI() (Client, error) {
	key := os.Getenv("OPENAI_API_KEY")
	endpoint := baseURL("OPENAI_BASE_URL")
	if key == "" && endpoint == "" {
		return nil, fmt.Errorf("%w: set OPENAI_API_KEY or a base URL", ErrNoProvider)
	}

	options := []option.RequestOption{option.WithAPIKey(key)}
	if endpoint != "" {
		options = append(options, option.WithBaseURL(endpoint))
	}
	return &openAI{
		client: openai.NewClient(options...),
		model:  env("OPENAI_MODEL", defaultOpenAIModel),
	}, nil
}

func (openAI *openAI) Name() string { return "openai/" + openAI.model }

func (openAI *openAI) Complete(ctx context.Context, system, user string) (string, error) {
	response, err := openAI.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:               openAI.model,
		MaxCompletionTokens: openai.Int(openAIMaxTokens),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(system),
			openai.UserMessage(user),
		},
	})
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
