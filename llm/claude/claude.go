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
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/mukezhz/code-mode/llm"
)

func init() {
	llm.RegisterProvider(llm.Provider{
		Name:    llm.ProviderClaude,
		Aliases: []string{"anthropic"},
		New:     New,
		// LLM_BASE_URL names no provider, so it must not select this one;
		// only Anthropic's own variables count as configuration.
		Configured: func() bool {
			return os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("ANTHROPIC_BASE_URL") != ""
		},
		Priority: 1,
	})
}

// defaultClaudeModel is used when ANTHROPIC_MODEL is unset.
const defaultClaudeModel = "claude-opus-5"

// claudeMaxTokens bounds thinking and response text together. Claude Opus 5
// thinks by default, so a budget sized only for the program would truncate it.
const claudeMaxTokens = 16000

type claude struct {
	client anthropic.Client
	model  string
	// fallbacks is off against a custom endpoint, which may not implement the
	// server-side fallback beta.
	fallbacks bool
}

// New builds a Claude client from the environment. Blank-import this package
// instead to have llm.New select it from LLM_PROVIDER.
//
// Environment: ANTHROPIC_API_KEY, ANTHROPIC_MODEL, ANTHROPIC_BASE_URL.
func New() (llm.Client, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	endpoint := llm.BaseURL("ANTHROPIC_BASE_URL")
	if key == "" && endpoint == "" {
		return nil, fmt.Errorf("%w: set ANTHROPIC_API_KEY or a base URL", llm.ErrNoProvider)
	}

	options := []option.RequestOption{option.WithAPIKey(key)}
	if endpoint != "" {
		options = append(options, option.WithBaseURL(endpoint))
	}
	return &claude{
		client:    anthropic.NewClient(options...),
		model:     llm.Env("ANTHROPIC_MODEL", defaultClaudeModel),
		fallbacks: endpoint == "",
	}, nil
}

func (claude *claude) Name() string { return "claude/" + claude.model }

func (claude *claude) Complete(ctx context.Context, system, user string) (string, error) {
	params := anthropic.BetaMessageNewParams{
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
	}
	if claude.fallbacks {
		// A safety classifier may decline; the recommended fallback model
		// serves the request instead of the call failing.
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
	}

	response, err := claude.client.Beta.Messages.New(ctx, params)
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
