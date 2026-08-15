// Package llm turns a code mode prompt into a model-authored program.
//
// The provider is selected by LLM_PROVIDER so the rest of the system depends on
// the Client interface rather than on a vendor SDK.
//
// # Choosing a provider
//
// This package holds the interface, not the SDKs. Blank-import the providers
// you want and they register themselves, so a binary links only the SDKs it
// can actually call:
//
//	import (
//		"github.com/mukezhz/code-mode/llm"
//		_ "github.com/mukezhz/code-mode/llm/claude"
//	)
//
//	model, err := llm.New()  // LLM_PROVIDER picks one
//
// Importing a provider directly works too, and skips the environment entirely.
package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
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

// Provider describes one registered model backend.
type Provider struct {
	// Name is the canonical value of LLM_PROVIDER that selects it.
	Name string
	// Aliases are other accepted spellings, such as "anthropic" for claude.
	Aliases []string
	// New builds a Client. Options given by the caller win over the
	// environment; a zero Options is entirely environment-driven.
	New func(Options) (Client, error)
	// Configured reports whether this provider's own environment variables are
	// set, so New can pick one when LLM_PROVIDER is unset.
	Configured func() bool
	// Priority orders detection when more than one provider is configured.
	// Lower wins. It is explicit rather than registration order, because
	// import order is not something a caller should have to reason about.
	Priority int
}

// Providers this module ships, and the package that registers each.
const (
	// ProviderClaude speaks the Anthropic API. Package llm/claude.
	ProviderClaude = "claude"
	// ProviderOpenAI speaks the OpenAI API, which most self-hosted and
	// third-party endpoints also speak. Package llm/openai.
	ProviderOpenAI = "openai"
)

var shipped = map[string]string{
	ProviderClaude: "github.com/mukezhz/code-mode/llm/claude",
	ProviderOpenAI: "github.com/mukezhz/code-mode/llm/openai",
}

var (
	providersMutex sync.RWMutex
	providers      = map[string]Provider{}
)

// RegisterProvider makes provider selectable by New. Provider packages call it
// from init, so blank-importing one is enough.
func RegisterProvider(provider Provider) {
	if provider.Name == "" || provider.New == nil {
		panic("llm: provider needs a name and a constructor")
	}
	providersMutex.Lock()
	defer providersMutex.Unlock()
	for _, name := range append([]string{provider.Name}, provider.Aliases...) {
		if _, exists := providers[name]; exists {
			panic("llm: provider " + name + " is already registered")
		}
		providers[name] = provider
	}
}

// RegisteredProviders lists the canonical names of every registered provider,
// sorted.
func RegisteredProviders() []string {
	providersMutex.RLock()
	defer providersMutex.RUnlock()
	unique := map[string]bool{}
	for _, provider := range providers {
		unique[provider.Name] = true
	}
	names := make([]string, 0, len(unique))
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// New builds a client from options, falling back to the environment for every
// field left zero. Options.Provider, then LLM_PROVIDER, names the provider.
// Any endpoint speaking either wire protocol works by setting a base URL, so
// self-hosted and third-party models are reached through the same providers.
// It returns ErrNoProvider when nothing is configured.
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
func New(options Options) (Client, error) {
	name := strings.ToLower(strings.TrimSpace(options.Provider))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))
	}
	if name != "" {
		providersMutex.RLock()
		provider, registered := providers[name]
		providersMutex.RUnlock()
		if !registered {
			return nil, unregisteredProvider(name)
		}
		return provider.New(options)
	}

	// Unset means "whichever provider is configured", so a single variable is
	// enough to get started.
	if provider, found := detect(); found {
		return provider.New(options)
	}
	if len(RegisteredProviders()) == 0 {
		return nil, fmt.Errorf("%w: no providers are registered, blank-import %s or %s",
			ErrNoProvider, shipped[ProviderClaude], shipped[ProviderOpenAI])
	}
	return nil, fmt.Errorf("%w: set ANTHROPIC_API_KEY, OPENAI_API_KEY, or LLM_BASE_URL", ErrNoProvider)
}

// detect returns the configured provider with the lowest Priority. A provider
// with no Configured func never self-selects, so registering one cannot change
// which provider an existing deployment picks.
func detect() (Provider, bool) {
	providersMutex.RLock()
	defer providersMutex.RUnlock()

	var best Provider
	found := false
	for _, provider := range providers {
		if provider.Configured == nil || !provider.Configured() {
			continue
		}
		if !found || provider.Priority < best.Priority {
			best, found = provider, true
		}
	}
	return best, found
}

func unregisteredProvider(name string) error {
	if path, ships := shipped[name]; ships {
		return fmt.Errorf("LLM_PROVIDER %q is not registered: add `_ %q` to your imports", name, path)
	}
	// "anthropic" is an alias, so a bare name check is not enough to tell an
	// unregistered shipped provider from a typo.
	available := RegisteredProviders()
	if len(available) == 0 {
		return fmt.Errorf("LLM_PROVIDER %q is unknown and no providers are registered: blank-import %s or %s",
			name, shipped[ProviderClaude], shipped[ProviderOpenAI])
	}
	return fmt.Errorf("LLM_PROVIDER %q is unknown: registered providers are %s",
		name, strings.Join(available, ", "))
}

// Env returns the value of name, or fallback when it is unset or blank.
// Provider packages use it so every provider reads configuration the same way.
func Env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// BaseURL returns the provider-specific base URL named by name, falling back to
// LLM_BASE_URL. An empty name consults only LLM_BASE_URL.
func BaseURL(name string) string {
	if name != "" {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return strings.TrimSpace(os.Getenv("LLM_BASE_URL"))
}

// Embedder turns text into a vector for capability search.
//
// Embedders are not registered the way providers are: only one embedding
// protocol is in play, the OpenAI one, so llm/openai's NewEmbedder is the only
// constructor and any compatible endpoint is reached by base URL.
type Embedder struct {
	// Model is recorded in the capability index, so a later search with a
	// different model is rejected rather than silently mismatched.
	Model string
	// Embed produces the vector. Ctx carries cancellation.
	Embed func(ctx context.Context, text string) ([]float32, error)
}
