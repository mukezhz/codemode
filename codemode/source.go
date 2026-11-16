package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/mukezhz/codemode/capability"
)

// Source is what every capability adapter produces: the capabilities, and the
// function that serves their RPCs.
type Source struct {
	Registry capability.Registry
	Execute  ExecuteFunc
	// Skipped holds definitions the adapter could not express, with the reason.
	Skipped map[string]error
}

// Combine serves several sources as one. A capability name or RPC defined twice is an error.
func Combine(sources ...Source) (Source, error) {
	registries := make([]capability.Registry, len(sources))
	owner := map[string]ExecuteFunc{}
	skipped := map[string]error{}
	for index, source := range sources {
		if source.Execute == nil {
			return Source{}, fmt.Errorf("source %d has no Execute", index)
		}
		registries[index] = source.Registry
		for _, item := range source.Registry.Capabilities {
			if _, exists := owner[item.RPC]; exists {
				return Source{}, fmt.Errorf("RPC %q is served by more than one source", item.RPC)
			}
			owner[item.RPC] = source.Execute
		}
		maps.Copy(skipped, source.Skipped)
	}
	merged, err := capability.Merge(registries...)
	if err != nil {
		return Source{}, err
	}
	return Source{
		Registry: merged,
		Skipped:  skipped,
		Execute: func(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
			execute, ok := owner[rpc]
			if !ok {
				return nil, fmt.Errorf("no source serves RPC %q", rpc)
			}
			return execute(ctx, rpc, input)
		},
	}, nil
}

// FromSource builds the runtime for one caller: only what caller may call,
// searched by keyword. Use New for a different search.
func FromSource(source Source, caller capability.Caller) (CodeMode, error) {
	if source.Execute == nil {
		return nil, errors.New("source has no Execute")
	}
	allowed := source.Registry.For(caller)
	return New(allowed, capability.Keyword(allowed), source.Execute)
}
