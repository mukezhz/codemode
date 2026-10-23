package prompt

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mukezhz/codemode/capability"
)

type Dialect struct {
	ID        string
	Prose     string
	Rules     []string
	Example   string
	Signature func(capability.Capability) string
	Comment   func(string) string
	Shape     func(name string, schema capability.TypeSchema) string
}

func (dialect Dialect) Render(matches []capability.Match) string {
	var builder strings.Builder

	fmt.Fprintf(&builder,
		"You write short %s programs that answer a user's request by calling application capabilities.\n\nRules:\n",
		dialect.Prose)
	for _, rule := range dialect.Rules {
		builder.WriteString("- " + rule + "\n")
	}
	for _, rule := range []string{
		"Only the capabilities declared below exist. Any other name is rejected.",
		"Do the filtering, looping, and composing yourself rather than asking for one result at a time.",
		"If a capability call fails, let it fail; do not swallow errors.",
		"There is no console, filesystem, network, or module loader. codemode.execute is the only host binding.",
	} {
		builder.WriteString("- " + rule + "\n")
	}

	builder.WriteString("\nReply with the program only. No prose, no markdown fences.\n\nDeclared capabilities:\n\n")
	builder.WriteString(dialect.Declarations(matches))
	if types := dialect.Types(matches); types != "" {
		builder.WriteString("Shapes referred to above:\n\n")
		builder.WriteString(types)
	}
	builder.WriteString("\nExample of the shape expected:\n\n")
	builder.WriteString(strings.TrimSpace(dialect.Example))
	builder.WriteString("\n")

	return builder.String()
}

func (dialect Dialect) Declarations(matches []capability.Match) string {
	var builder strings.Builder
	for _, match := range matches {
		item := match.Capability
		lines := append([]string{item.Description}, notes("input", item.Input)...)
		for _, line := range append(lines, notes("output", item.Output)...) {
			builder.WriteString(dialect.Comment(line))
			builder.WriteString("\n")
		}
		builder.WriteString(dialect.Signature(item))
		builder.WriteString("\n\n")
	}
	return builder.String()
}

// Types renders each message shape the declarations refer to, once, in sorted order for cache stability.
func (dialect Dialect) Types(matches []capability.Match) string {
	shapes := map[string]capability.TypeSchema{}
	for _, match := range matches {
		for name, schema := range match.Capability.Types {
			shapes[name] = schema
		}
	}
	if len(shapes) == 0 {
		return ""
	}

	names := make([]string, 0, len(shapes))
	for name := range shapes {
		names = append(names, name)
	}
	sort.Strings(names)

	var builder strings.Builder
	for _, name := range names {
		schema := shapes[name]
		if len(schema.Values) > 0 {
			builder.WriteString(dialect.Comment(name + " is one of " + quoted(schema.Values)))
			builder.WriteString("\n")
			continue
		}
		for _, line := range notes(name, schema) {
			builder.WriteString(dialect.Comment(line))
			builder.WriteString("\n")
		}
		builder.WriteString(dialect.Shape(name, schema))
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	return builder.String()
}

func notes(owner string, schema capability.TypeSchema) []string {
	var lines []string
	for _, field := range sortedKeys(schema.Descriptions) {
		lines = append(lines, owner+"."+field+": "+schema.Descriptions[field])
	}
	for _, group := range sortedKeys(schema.Oneofs) {
		lines = append(lines, owner+": set at most one of "+strings.Join(schema.Oneofs[group], ", "))
	}
	return lines
}

func quoted(values []string) string {
	rendered := make([]string, len(values))
	for i, value := range values {
		rendered[i] = fmt.Sprintf("%q", value)
	}
	return strings.Join(rendered, ", ")
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type Types struct {
	// Scalar maps a neutral scalar name to this dialect's; absent names pass through.
	Scalar map[string]string
	List   func(element string) string
	Dict   func(key, value string) string
	// Marks a field the caller may leave out and a response may omit.
	Optional func(fieldType string) string
}

// Render converts one neutral type; protobuf forbids nesting, so shapes never nest.
func (style Types) Render(neutral string) string {
	if element, found := strings.CutPrefix(neutral, "repeated "); found {
		return style.List(style.Render(element))
	}
	if inner, found := strings.CutPrefix(neutral, "map<"); found {
		if inner, found := strings.CutSuffix(inner, ">"); found {
			// A map key is always a scalar, so the first separator splits it.
			if key, value, found := strings.Cut(inner, ", "); found {
				return style.Dict(style.Render(key), style.Render(value))
			}
		}
	}
	if scalar, found := style.Scalar[neutral]; found {
		return scalar
	}
	return neutral
}

// Fields renders a schema's fields, sorted so the prompt stays byte-stable.
func Fields(schema capability.TypeSchema, style Types, format func(name, fieldType string) string, empty, open, close string) string {
	if len(schema.Fields) == 0 {
		return empty
	}
	rendered := make([]string, 0, len(schema.Fields))
	for name, fieldType := range schema.Fields {
		fieldType = style.Render(fieldType)
		if slices.Contains(schema.Optional, name) {
			fieldType = style.Optional(fieldType)
		}
		rendered = append(rendered, format(name, fieldType))
	}
	sort.Strings(rendered)
	return open + strings.Join(rendered, ", ") + close
}
