// Package llm turns a code mode prompt into a model-authored program.
//
// The provider sits behind the Client interface, so the rest of the system
// depends on it rather than on a vendor SDK.
//
// # Configuration
//
// Everything is configured through llm.Options. This package reads no
// environment variable of its own: your application already has a way to
// configure itself, and a library that reaches into os.Getenv fights it.
//
//	model, err := llm.New(llm.Options{
//		Provider: llm.ProviderClaude,
//		APIKey:   config.AnthropicKey,
//	})
//
// If you do want the environment consulted, ask for it: OptionsFromEnv returns
// an Options you can inspect and override, and NewFromEnv is the one-liner. The
// rule holds across the whole library — only a name containing FromEnv reads
// the environment.
//
// # Choosing a provider
//
// This package holds the interface, not the SDKs. Blank-import the providers
// you want and they register themselves, so a binary links only the SDKs it can
// actually call:
//
//	import (
//		"github.com/mukezhz/code-mode/llm"
//		_ "github.com/mukezhz/code-mode/llm/claude"
//	)
//
// Importing a provider directly and calling its own New skips this package's
// selection entirely, and is the most explicit form.
package llm

import (
	"context"
	"errors"
	"fmt"
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
	// Name is the canonical name that selects it, such as "claude".
	Name string
	// Aliases are other accepted spellings, such as "anthropic".
	Aliases []string
	// New builds a Client from options.
	New func(Options) (Client, error)
	// FromEnv reads this provider's own environment variables and reports
	// whether any were set. Only OptionsFromEnv calls it; a provider that
	// leaves it nil is selectable by name but never found by env detection.
	FromEnv func() (Options, bool, error)
	// Priority orders env detection when more than one provider is configured.
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

// New builds the client named by options.Provider. It reads no environment
// variable; every value comes from options. Use OptionsFromEnv first if you
// want the environment consulted.
func New(options Options) (Client, error) {
	name := strings.ToLower(strings.TrimSpace(options.Provider))
	if name == "" {
		return nil, fmt.Errorf("%w: set Options.Provider, or read one from the environment with llm.NewFromEnv", ErrNoProvider)
	}

	provider, found := lookup(name)
	if !found {
		return nil, unregisteredProvider(name)
	}
	return provider.New(options)
}

func lookup(name string) (Provider, bool) {
	providersMutex.RLock()
	defer providersMutex.RUnlock()
	provider, found := providers[name]
	return provider, found
}

// byPriority returns each registered provider once, lowest Priority first.
func byPriority() []Provider {
	providersMutex.RLock()
	defer providersMutex.RUnlock()
	list := make([]Provider, 0, len(providers))
	seen := map[string]bool{}
	for _, provider := range providers {
		if seen[provider.Name] {
			continue
		}
		seen[provider.Name] = true
		list = append(list, provider)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Priority != list[j].Priority {
			return list[i].Priority < list[j].Priority
		}
		return list[i].Name < list[j].Name
	})
	return list
}

func unregisteredProvider(name string) error {
	if path, ships := shipped[name]; ships {
		return fmt.Errorf("LLM provider %q is not registered: add `_ %q` to your imports", name, path)
	}
	available := RegisteredProviders()
	if len(available) == 0 {
		return fmt.Errorf("LLM provider %q is unknown and none are registered: blank-import %s or %s",
			name, shipped[ProviderClaude], shipped[ProviderOpenAI])
	}
	return fmt.Errorf("LLM provider %q is unknown: registered providers are %s",
		name, strings.Join(available, ", "))
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
