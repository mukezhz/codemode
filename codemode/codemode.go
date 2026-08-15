package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mukezhz/codemode/capability"
)

// CodeMode discovers application capabilities and executes an explicitly named
// capability through a trusted transport.
type CodeMode interface {
	// Search finds the capabilities most relevant to query. Ctx carries the
	// request deadline and cancellation; limit is the maximum number of matches
	// to return. Matches are ordered from closest to least close; a lower
	// Match.Distance means a stronger match.
	Search(ctx context.Context, query string, limit int) ([]capability.Match, error)

	// Execute invokes the registered capability named name, such as
	// "todos.complete". Ctx carries the request deadline and cancellation; input
	// is the capability request encoded as JSON. The returned RawMessage is its
	// JSON response. The runtime resolves name to the trusted protobuf RPC path
	// before calling the configured ExecuteFunc.
	Execute(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error)
}

// SearchFunc implements capability ranking. Query describes the user's intent,
// ctx carries cancellation, and limit is the maximum number of matches.
type SearchFunc func(ctx context.Context, query string, limit int) ([]capability.Match, error)

// ExecuteFunc invokes a trusted transport. RPC is the protobuf path resolved
// from the registry, ctx carries cancellation, and input is the JSON request.
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
