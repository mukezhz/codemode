// Package prompt holds the prompt frame shared by this module's sandbox engines.
package prompt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mukezhz/codemode/capability"
)

// Dialect describes how programs are written for one engine.
type Dialect struct {
	// ID is the short, stable name of the language that callers key on.
	ID string
	// Prose is how the language is described to a model.
	Prose string
	// Rules are the engine-specific instructions, one per line.
	Rules []string
	// Example is a short, complete program in this dialect.
	Example string
	// Signature renders one capability as a call the model can copy.
	Signature func(capability.Capability) string
	// Comment wraps a description in this dialect's comment syntax.
	Comment func(string) string
	// Shape renders one referenced message type as a named object.
	Shape func(name string, schema capability.TypeSchema) string
}

// Render produces the shared frame around a dialect's rules and the declarations for matches.
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

// Declarations renders each match as a commented call signature.
func (dialect Dialect) Declarations(matches []capability.Match) string {
	var builder strings.Builder
	for _, match := range matches {
		builder.WriteString(dialect.Comment(match.Capability.Description))
		builder.WriteString("\n")
		builder.WriteString(dialect.Signature(match.Capability))
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
		builder.WriteString(dialect.Shape(name, shapes[name]))
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	return builder.String()
}

// Types renders the registry's language-neutral field types into one dialect's notation.
type Types struct {
	// Scalar maps a neutral scalar name to this dialect's; absent names pass through.
	Scalar map[string]string
	// List renders a repeated field given its already-rendered element type.
	List func(element string) string
	// Dict renders a map field given its already-rendered key and value types.
	Dict func(key, value string) string
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
		rendered = append(rendered, format(name, style.Render(fieldType)))
	}
	sort.Strings(rendered)
	return open + strings.Join(rendered, ", ") + close
}
