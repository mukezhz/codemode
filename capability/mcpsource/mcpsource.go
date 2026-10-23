// Package mcpsource imports MCP tools as capabilities; each requires a role and is treated as a write.
package mcpsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
)

type Source struct {
	Registry capability.Registry
	// Execute serves only RPCs from this source; route the rest to your own ExecuteFunc.
	Execute codemode.ExecuteFunc
	// Skipped holds tools whose schemas could not be expressed, with the reason.
	Skipped map[string]error
}

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
	base := pascal(prefix) + pascal(tool.Name)

	input, err := object(tool.InputSchema, base+"Input", types)
	if err != nil {
		return capability.Capability{}, fmt.Errorf("input: %w", err)
	}
	output := capability.TypeSchema{Fields: map[string]string{"text": "string"}}
	if tool.OutputSchema != nil {
		if output, err = object(tool.OutputSchema, base+"Output", types); err != nil {
			return capability.Capability{}, fmt.Errorf("output: %w", err)
		}
	}
	if len(types) == 0 {
		types = nil
	}
	return capability.Capability{
		Name:        prefix + "." + tool.Name,
		RPC:         RPCPrefix(prefix) + tool.Name,
		Description: tool.Description,
		Input:       input,
		Output:      output,
		Types:       types,
	}, nil
}

type schema struct {
	Type        any                `json:"type"`
	Description string             `json:"description"`
	Properties  map[string]*schema `json:"properties"`
	Required    []string           `json:"required"`
	Items       *schema            `json:"items"`
	Enum        []any              `json:"enum"`
}

// Errors rather than loosening to "any", which would mislead the model.
func object(raw any, name string, types map[string]capability.TypeSchema) (capability.TypeSchema, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return capability.TypeSchema{}, err
	}
	var parsed schema
	if err := json.Unmarshal(encoded, &parsed); err != nil {
		return capability.TypeSchema{}, err
	}
	if kind, _ := single(parsed.Type); kind != "object" {
		return capability.TypeSchema{}, fmt.Errorf("top-level schema is %v, not an object", parsed.Type)
	}
	return fields(&parsed, name, types)
}

func fields(parsed *schema, name string, types map[string]capability.TypeSchema) (capability.TypeSchema, error) {
	result := capability.TypeSchema{Type: name, Fields: map[string]string{}}
	descriptions := map[string]string{}
	for field, property := range parsed.Properties {
		kind, err := typeOf(property, name+pascal(field), types)
		if err != nil {
			return capability.TypeSchema{}, fmt.Errorf("%s: %w", field, err)
		}
		result.Fields[field] = kind
		if description := strings.TrimSpace(property.Description); description != "" {
			descriptions[field] = description
		}
		if !slices.Contains(parsed.Required, field) {
			result.Optional = append(result.Optional, field)
		}
	}
	sort.Strings(result.Optional)
	if len(descriptions) > 0 {
		result.Descriptions = descriptions
	}
	return result, nil
}

func typeOf(property *schema, name string, types map[string]capability.TypeSchema) (string, error) {
	if property == nil {
		return "", errors.New("no schema")
	}
	kind, err := single(property.Type)
	if err != nil {
		return "", err
	}
	switch kind {
	case "string":
		if len(property.Enum) == 0 {
			return "string", nil
		}
		values := make([]string, len(property.Enum))
		for i, value := range property.Enum {
			text, ok := value.(string)
			if !ok {
				return "", fmt.Errorf("enum value %v is not a string", value)
			}
			values[i] = text
		}
		types[name] = capability.TypeSchema{Type: name, Values: values}
		return name, nil
	case "integer", "number":
		return "number", nil
	case "boolean":
		return "bool", nil
	case "array":
		element, err := typeOf(property.Items, name+"Item", types)
		if err != nil {
			return "", fmt.Errorf("items: %w", err)
		}
		return "repeated " + element, nil
	case "object":
		nested, err := fields(property, name, types)
		if err != nil {
			return "", err
		}
		types[name] = nested
		return name, nil
	}
	return "", fmt.Errorf("unsupported type %v", property.Type)
}

// single accepts "string" or ["string", "null"]: nullable is still one type.
func single(kind any) (string, error) {
	switch kind := kind.(type) {
	case string:
		return kind, nil
	case []any:
		var found []string
		for _, item := range kind {
			if name, ok := item.(string); ok && name != "null" {
				found = append(found, name)
			}
		}
		if len(found) == 1 {
			return found[0], nil
		}
	}
	return "", fmt.Errorf("type %v is not a single JSON type", kind)
}

func pascal(name string) string {
	var builder strings.Builder
	upper := true
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			upper = true
			continue
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		builder.WriteRune(r)
	}
	return builder.String()
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
