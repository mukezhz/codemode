// Package llm turns a code mode prompt into a model-authored program via a
// pluggable Client; providers live in subpackages and register from init.
package llm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Client completes a single prompt.
type Client interface {
	// Complete returns the model's response text.
	Complete(ctx context.Context, system, user string) (string, error)

	// Name identifies the provider and model.
	Name() string
}

// Streamer is an optional Client extension that delivers the response in pieces.
type Streamer interface {
	// CompleteStream returns the same text Complete would, calling chunk with
	// each fragment; chunk runs on the calling goroutine, must not block, and may be nil.
	CompleteStream(ctx context.Context, system, user string, chunk func(string)) (string, error)
}

// ErrNoProvider reports that no provider is configured.
var ErrNoProvider = errors.New("no LLM provider configured")

// Provider describes one registered model backend.
type Provider struct {
	// Name is the canonical name that selects it.
	Name string
	// Aliases are other accepted spellings.
	Aliases []string
	// New builds a Client from options.
	New func(Options) (Client, error)
	// FromEnv reads this provider's environment variables and reports whether
	// any were set; nil excludes it from env detection.
	FromEnv func() (Options, bool, error)
	// Priority orders env detection; lower wins.
	Priority int
}

// Providers this module ships.
const (
	// ProviderClaude speaks the Anthropic API (llm/claude).
	ProviderClaude = "claude"
	// ProviderOpenAI speaks the OpenAI-compatible API (llm/openai).
	ProviderOpenAI = "openai"
)

var shipped = map[string]string{
	ProviderClaude: "github.com/mukezhz/codemode/llm/claude",
	ProviderOpenAI: "github.com/mukezhz/codemode/llm/openai",
}

var (
	providersMutex sync.RWMutex
	providers      = map[string]Provider{}
)

// RegisterProvider makes provider selectable by New; registering a name twice panics.
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

// RegisteredProviders lists the canonical names of every registered provider, sorted.
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

// New builds the client named by options.Provider without reading the environment.
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
type Embedder struct {
	// Model is recorded in the capability index so mismatched searches are rejected.
	Model string
	// Embed produces the vector.
	Embed func(ctx context.Context, text string) ([]float32, error)
}
