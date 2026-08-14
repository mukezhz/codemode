// Package llm turns a code mode prompt into a model-authored program.
//
// The provider is selected by LLM_PROVIDER so the rest of the system depends on
// the Client interface rather than on a vendor SDK.
package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Client completes a single prompt. System states the rules and the available
// capabilities; user carries the request being served.
type Client interface {
	// Complete returns the model's response text. Ctx carries the request
	// deadline and cancellation.
	Complete(ctx context.Context, system, user string) (string, error)

	// Name identifies the provider and model, for logs and error messages.
	Name() string
}

// ErrNoProvider reports that no provider is configured. Callers that have a
// fallback path, such as demos, can check for it with errors.Is.
var ErrNoProvider = errors.New("no LLM provider configured")

// New builds the client named by LLM_PROVIDER: "claude" (the default) or
// "openai". It returns ErrNoProvider when neither provider has credentials.
//
// Environment:
//
//	LLM_PROVIDER     claude | openai            (default claude)
//	ANTHROPIC_API_KEY, ANTHROPIC_MODEL          (model default claude-opus-5)
//	OPENAI_API_KEY, OPENAI_MODEL                (model default gpt-5)
func New() (Client, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))
	switch provider {
	case "claude", "anthropic":
		return newClaude()
	case "openai":
		return newOpenAI()
	case "":
		// Unset means "whichever key is present", so the demo runs without
		// asking the user to set two variables.
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			return newClaude()
		}
		if os.Getenv("OPENAI_API_KEY") != "" {
			return newOpenAI()
		}
		return nil, fmt.Errorf("%w: set ANTHROPIC_API_KEY or OPENAI_API_KEY", ErrNoProvider)
	default:
		return nil, fmt.Errorf("LLM_PROVIDER %q is not supported: use claude or openai", provider)
	}
}

// env returns the value of name, or fallback when it is unset or blank.
func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
