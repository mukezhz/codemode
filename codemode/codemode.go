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
	"time"

	"github.com/mukezhz/codemode/capability"
)

type CodeMode interface {
	Search(ctx context.Context, query string, limit int) ([]capability.Match, error)

	// Unregistered names are rejected.
	Execute(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error)
}

// The identifier scopes cached programs.
type Fingerprinter interface {
	Fingerprint() string
}

// Hashes each full contract so a schema, role, or effect change invalidates cached programs.
func (runtime *runtime) Fingerprint() string {
	names := make([]string, 0, len(runtime.capabilities))
	for name := range runtime.capabilities {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		contract, _ := json.Marshal(runtime.capabilities[name])
		hash.Write(contract)
		hash.Write([]byte{0x1e})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type SearchFunc func(ctx context.Context, query string, limit int) ([]capability.Match, error)

type ExecuteFunc func(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error)

type runtime struct {
	capabilities map[string]capability.Capability
	search       SearchFunc
	execute      ExecuteFunc
}

// registry is the executable capability allowlist.
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
	recorder := recorderFrom(ctx)
	if recorder == nil {
		return runtime.call(ctx, item, input)
	}
	call := Call{Name: name, Input: input, ReadOnly: item.ReadOnly}
	if recorder.Plan && !item.ReadOnly {
		call.Planned = true
		call.Output = json.RawMessage("{}")
		recorder.add(call)
		return call.Output, nil
	}
	started := time.Now()
	output, err := runtime.call(ctx, item, input)
	call.Duration = time.Since(started)
	call.Output = output
	if err != nil {
		call.Error, call.err = err.Error(), err
	}
	recorder.add(call)
	return output, err
}

func (runtime *runtime) call(ctx context.Context, item capability.Capability, input json.RawMessage) (json.RawMessage, error) {
	output, err := runtime.execute(ctx, item.RPC, input)
	if err != nil {
		return nil, fmt.Errorf("execute %s: %w", item.Name, err)
	}
	return output, nil
}
