package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// defaultClaudeModel is used when ANTHROPIC_MODEL is unset.
const defaultClaudeModel = "claude-opus-5"

// claudeMaxTokens bounds thinking and response text together. Claude Opus 5
// thinks by default, so a budget sized only for the program would truncate it.
const claudeMaxTokens = 16000

type claude struct {
	client anthropic.Client
	model  string
}

func newClaude() (Client, error) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return nil, fmt.Errorf("%w: ANTHROPIC_API_KEY is not set", ErrNoProvider)
	}
	return &claude{
		client: anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY"))),
		model:  env("ANTHROPIC_MODEL", defaultClaudeModel),
	}, nil
}

func (claude *claude) Name() string { return "claude/" + claude.model }

func (claude *claude) Complete(ctx context.Context, system, user string) (string, error) {
	response, err := claude.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(claude.model),
		MaxTokens: claudeMaxTokens,
		System: []anthropic.BetaTextBlockParam{{
			Text: system,
			// The capability declarations are stable across a session, so
			// caching them keeps repeated requests cheap.
			CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(user)),
		},
		// A safety classifier may decline; the recommended fallback model
		// serves the request instead of the call failing.
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
	})
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
