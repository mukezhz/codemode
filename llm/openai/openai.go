// Package openai speaks the OpenAI API.
//
// Blank-import it to make LLM_PROVIDER=openai available:
//
//	import _ "github.com/mukezhz/code-mode/llm/openai"
//
// Most self-hosted and third-party endpoints speak this protocol, so pointing
// OPENAI_BASE_URL at one reaches it without another provider. It also holds
// NewEmbedder, which capability search uses regardless of which provider
// writes the programs.
package openai

import (
	"context"
	"fmt"
	"os"
	"strings"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/mukezhz/code-mode/llm"
)

func init() {
	llm.RegisterProvider(llm.Provider{
		Name: llm.ProviderOpenAI,
		New:  New,
		Configured: func() bool {
			// LLM_BASE_URL alone lands here: most self-hosted and third-party
			// endpoints speak this protocol, so it is the safer default.
			return os.Getenv("OPENAI_API_KEY") != "" ||
				os.Getenv("OPENAI_BASE_URL") != "" ||
				llm.BaseURL("") != ""
		},
		Priority: 2,
	})
}

// defaultOpenAIModel is used when OPENAI_MODEL is unset. Set that variable to
// pin whichever model your account is entitled to.
const defaultOpenAIModel = "gpt-5"

// openAIMaxTokens bounds reasoning and response text together.
const openAIMaxTokens = 16000

type client struct {
	client openaisdk.Client
	model  string
}

// New builds an OpenAI client from the environment. Blank-import this package
// instead to have llm.New select it from LLM_PROVIDER.
//
// Environment: OPENAI_API_KEY, OPENAI_MODEL, OPENAI_BASE_URL.
func New() (llm.Client, error) {
	key := os.Getenv("OPENAI_API_KEY")
	endpoint := llm.BaseURL("OPENAI_BASE_URL")
	if key == "" && endpoint == "" {
		return nil, fmt.Errorf("%w: set OPENAI_API_KEY or a base URL", llm.ErrNoProvider)
	}

	options := []option.RequestOption{option.WithAPIKey(key)}
	if endpoint != "" {
		options = append(options, option.WithBaseURL(endpoint))
	}
	return &client{
		client: openaisdk.NewClient(options...),
		model:  llm.Env("OPENAI_MODEL", defaultOpenAIModel),
	}, nil
}

func (client *client) Name() string { return "openai/" + client.model }

func (client *client) Complete(ctx context.Context, system, user string) (string, error) {
	response, err := client.client.Chat.Completions.New(ctx, openaisdk.ChatCompletionNewParams{
		Model:               client.model,
		MaxCompletionTokens: openaisdk.Int(openAIMaxTokens),
		Messages: []openaisdk.ChatCompletionMessageParamUnion{
			openaisdk.SystemMessage(system),
			openaisdk.UserMessage(user),
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
