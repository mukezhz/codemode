// Package sourcetest is the contract every capability source passes:
// call Run from the adapter's own tests.
package sourcetest

import (
	"context"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
)

var scalars = map[string]bool{"string": true, "bytes": true, "bool": true, "number": true, "integer": true, "any": true}

func Run(t *testing.T, source codemode.Source) {
	t.Helper()
	if source.Execute == nil {
		t.Fatal("source has no Execute")
	}
	if len(source.Registry.Capabilities) == 0 {
		t.Fatal("source has no capabilities")
	}
	names, rpcs := map[string]bool{}, map[string]bool{}
	for _, item := range source.Registry.Capabilities {
		if names[item.Name] || rpcs[item.RPC] {
			t.Errorf("%s: name or RPC %q appears twice", item.Name, item.RPC)
		}
		names[item.Name], rpcs[item.RPC] = true, true
		if strings.TrimSpace(item.Description) == "" {
			t.Errorf("%s: no description; search and the model have nothing to go on", item.Name)
		}
		if item.Input.Fields == nil {
			t.Errorf("%s: input shape is not declared, so its input is never validated", item.Name)
		}
		if item.ReadOnly && item.NeedsApproval {
			t.Errorf("%s: read-only and needs approval", item.Name)
		}
		for owner, fields := range map[string]map[string]string{"input": item.Input.Fields, "output": item.Output.Fields} {
			for field, neutral := range fields {
				resolve(t, item.Name+" "+owner+"."+field, neutral, item.Types)
			}
		}
		for typeName, schema := range item.Types {
			for field, neutral := range schema.Fields {
				resolve(t, item.Name+" "+typeName+"."+field, neutral, item.Types)
			}
		}
	}
	if _, err := source.Execute(context.Background(), "sourcetest/no-such-rpc", nil); err == nil {
		t.Error("an unknown RPC did not fail")
	}
}

func resolve(t *testing.T, path, neutral string, types map[string]capability.TypeSchema) {
	t.Helper()
	if element, found := strings.CutPrefix(neutral, "repeated "); found {
		resolve(t, path, element, types)
		return
	}
	if inner, found := strings.CutPrefix(neutral, "map<"); found {
		if _, value, found := strings.Cut(strings.TrimSuffix(inner, ">"), ", "); found {
			resolve(t, path, value, types)
			return
		}
	}
	if scalars[neutral] {
		return
	}
	if _, declared := types[neutral]; !declared {
		t.Errorf("%s: type %s is not declared", path, neutral)
	}
}
