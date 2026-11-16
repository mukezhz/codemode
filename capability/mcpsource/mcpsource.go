// Package mcpsource imports MCP tools as capabilities; each requires a role and is treated as a write.
package mcpsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/capability/jsonschema"
	"github.com/mukezhz/codemode/codemode"
)

// Source.Execute serves only RPCs from this source; combine it with others through codemode.Combine.
type Source = codemode.Source

// RPCPrefix starts the RPC of every capability imported under prefix.
func RPCPrefix(prefix string) string { return "mcp/" + prefix + "/" }

// Import lists session's tools as prefix.<tool> capabilities, each requiring role.
func Import(ctx context.Context, session *mcp.ClientSession, prefix, role string) (Source, error) {
	if strings.TrimSpace(role) == "" {
		return Source{}, errors.New("mcpsource: a role is required; imported tools are never unrestricted")
	}
	if prefix == "" || strings.ContainsAny(prefix, "./ ") {
		return Source{}, fmt.Errorf("mcpsource: prefix %q must be a non-empty word", prefix)
	}

	source := Source{Skipped: map[string]error{}}
	imported := capability.Registry{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return Source{}, fmt.Errorf("mcpsource: list tools: %w", err)
		}
		item, err := convert(prefix, tool)
		if err != nil {
			source.Skipped[tool.Name] = err
			continue
		}
		imported.Capabilities = append(imported.Capabilities, item)
	}
	source.Registry = imported.Require(role, func(capability.Capability) bool { return true })
	source.Execute = execute(session, RPCPrefix(prefix))
	return source, nil
}

func convert(prefix string, tool *mcp.Tool) (capability.Capability, error) {
	if strings.ContainsAny(tool.Name, "./ ") {
		return capability.Capability{}, fmt.Errorf("tool name %q cannot be a capability name", tool.Name)
	}
	types := map[string]capability.TypeSchema{}
	base := jsonschema.Pascal(prefix) + jsonschema.Pascal(tool.Name)

	input, err := jsonschema.Object(tool.InputSchema, base+"Input", types)
	if err != nil {
		return capability.Capability{}, fmt.Errorf("input: %w", err)
	}
	output := capability.TypeSchema{Fields: map[string]string{"text": "string"}}
	if tool.OutputSchema != nil {
		if output, err = jsonschema.Object(tool.OutputSchema, base+"Output", types); err != nil {
			return capability.Capability{}, fmt.Errorf("output: %w", err)
		}
	}
	if len(types) == 0 {
		types = nil
	}
	return capability.Capability{
		Name:        prefix + "." + tool.Name,
		RPC:         RPCPrefix(prefix) + tool.Name,
		Description: description(tool),
		Input:       input,
		Output:      output,
		Types:       types,
	}, nil
}

// Search and the model need prose; a tool without a description falls back to its title or name.
func description(tool *mcp.Tool) string {
	for _, text := range []string{tool.Description, tool.Title} {
		if text = strings.TrimSpace(text); text != "" {
			return text
		}
	}
	return "MCP tool " + tool.Name
}

func execute(session *mcp.ClientSession, rpcPrefix string) codemode.ExecuteFunc {
	return func(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
		tool, ok := strings.CutPrefix(rpc, rpcPrefix)
		if !ok {
			return nil, fmt.Errorf("mcpsource: %s is not served by %s", rpc, rpcPrefix)
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: input})
		if err != nil {
			return nil, err
		}
		if result.IsError {
			return nil, errors.New(text(result))
		}
		if result.StructuredContent != nil {
			return json.Marshal(result.StructuredContent)
		}
		return json.Marshal(map[string]string{"text": text(result)})
	}
}

func text(result *mcp.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if content, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, content.Text)
		}
	}
	return strings.Join(parts, "\n")
}
