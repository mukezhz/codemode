package codemode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mukezhz/codemode/capability"
)

// CodeMode discovers application capabilities and executes an explicitly named
// capability through a trusted transport.
type CodeMode interface {
	// Search returns at most limit capabilities relevant to query, closest first.
	Search(ctx context.Context, query string, limit int) ([]capability.Match, error)

	// Execute invokes the registered capability name with JSON input; unregistered names are rejected.
	Execute(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error)
}

// Fingerprinter reports a stable identifier for the capability set a runtime
// will execute, used to scope cached programs.
type Fingerprinter interface {
	Fingerprint() string
}

// Fingerprint hashes the name and RPC path of every registered capability.
func (runtime *runtime) Fingerprint() string {
	names := make([]string, 0, len(runtime.capabilities))
	for name, item := range runtime.capabilities {
		names = append(names, name+"\x00"+item.RPC)
	}
	sort.Strings(names)
	sum := sha256.Sum256([]byte(strings.Join(names, "\x1e")))
	return hex.EncodeToString(sum[:])
}

// SearchFunc implements capability ranking.
type SearchFunc func(ctx context.Context, query string, limit int) ([]capability.Match, error)

// ExecuteFunc invokes a trusted transport with the protobuf RPC path resolved from the registry.
type ExecuteFunc func(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error)

type runtime struct {
	capabilities map[string]capability.Capability
	search       SearchFunc
	execute      ExecuteFunc
}

// New creates a CodeMode using registry as its executable capability allowlist,
// search for discovery, and execute for trusted RPC invocation.
func New(registry capability.Registry, search SearchFunc, execute ExecuteFunc) (CodeMode, error) {
	if search == nil {
		return nil, errors.New("search function is required")
	}
	if execute == nil {
		return nil, errors.New("execute function is required")
	}
	capabilities := make(map[string]capability.Capability, len(registry.Capabilities))
	for _, item := range registry.Capabilities {
		if strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.RPC) == "" {
			return nil, errors.New("capability name and RPC are required")
		}
		if _, exists := capabilities[item.Name]; exists {
			return nil, fmt.Errorf("duplicate capability %q", item.Name)
		}
		capabilities[item.Name] = item
	}
	return &runtime{capabilities: capabilities, search: search, execute: execute}, nil
}

func (runtime *runtime) Search(ctx context.Context, query string, limit int) ([]capability.Match, error) {
	return runtime.search(ctx, query, limit)
}

func (runtime *runtime) Execute(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	item, ok := runtime.capabilities[name]
	if !ok {
		return nil, fmt.Errorf("capability %q is not registered", name)
	}
	output, err := runtime.execute(ctx, item.RPC, input)
	if err != nil {
		return nil, fmt.Errorf("execute %s: %w", name, err)
	}
	return output, nil
}
