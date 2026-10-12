package llm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Client interface {
	Complete(ctx context.Context, system, user string) (string, error)

	Name() string
}

type Streamer interface {
	// chunk runs on the calling goroutine, must not block, and may be nil.
	CompleteStream(ctx context.Context, system, user string, chunk func(string)) (string, error)
}

var ErrNoProvider = errors.New("no LLM provider configured")

type Provider struct {
	Name    string
	Aliases []string
	New     func(Options) (Client, error)
	// FromEnv reports whether any of its variables were set; nil excludes it from env detection.
	FromEnv  func() (Options, bool, error)
	Priority int
}

const (
	ProviderClaude = "claude"
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

type Embedder struct {
	// Model is recorded in the capability index so mismatched searches are rejected.
	Model string
	Embed func(ctx context.Context, text string) ([]float32, error)
}
