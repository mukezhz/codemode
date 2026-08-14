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

// New builds the client named by LLM_PROVIDER: "claude" or "openai". Any
// endpoint speaking either wire protocol works by setting a base URL, so
// self-hosted and third-party models are reached through the same two
// providers. It returns ErrNoProvider when nothing is configured.
//
// Environment:
//
//	LLM_PROVIDER       claude | openai
//	LLM_BASE_URL       base URL for whichever provider is selected
//	ANTHROPIC_API_KEY, ANTHROPIC_MODEL, ANTHROPIC_BASE_URL
//	OPENAI_API_KEY, OPENAI_MODEL, OPENAI_BASE_URL
//
// The provider-specific base URL wins over LLM_BASE_URL. A base URL with no API
// key is allowed, for local endpoints that do not authenticate.
func New() (Client, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))
	switch provider {
	case "claude", "anthropic":
		return newClaude()
	case "openai":
		return newOpenAI()
	case "":
		// Unset means "whichever provider is configured", so a single
		// variable is enough to get started.
		// Provider-specific variables only: LLM_BASE_URL names no provider, so
		// letting it match here would always pick the first branch.
		switch {
		case os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("ANTHROPIC_BASE_URL") != "":
			return newClaude()
		case os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("OPENAI_BASE_URL") != "":
			return newOpenAI()
		case baseURL("") != "":
			// LLM_BASE_URL alone: most self-hosted and third-party endpoints
			// speak the OpenAI protocol, so that is the safer default.
			return newOpenAI()
		}
		return nil, fmt.Errorf("%w: set ANTHROPIC_API_KEY, OPENAI_API_KEY, or LLM_BASE_URL", ErrNoProvider)
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

// baseURL returns the provider-specific base URL named by name, falling back to
// LLM_BASE_URL. An empty name consults only LLM_BASE_URL.
func baseURL(name string) string {
	if name != "" {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return strings.TrimSpace(os.Getenv("LLM_BASE_URL"))
}
