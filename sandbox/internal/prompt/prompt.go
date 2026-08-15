// Package prompt holds the prompt frame shared by this module's sandbox
// engines. It is internal because the frame is an implementation detail of how
// these three engines phrase things, not a contract: a Runner implemented
// outside this module writes its own Prompt however it likes.
package prompt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mukezhz/codemode/capability"
)

// Dialect describes how programs are written for one engine. Everything a
// model needs to know that differs between engines lives here, so adding an
// engine does not touch the agent, the host, or any other engine.
type Dialect struct {
	// ID is the short, stable name of the language: "javascript", "starlark",
	// "lua". Callers key on this; it never changes wording.
	ID string
	// Prose is how the language is described to a model, which can be longer
	// and more explanatory than the ID.
	Prose string
	// Rules are the engine-specific instructions, one per line, inserted into
	// the shared prompt frame.
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

// Render produces the shared frame around a dialect's rules and the
// declarations for matches. The frame is identical across engines so a change
// to the house rules lands everywhere at once.
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

// Types renders each message shape the declarations refer to, once.
//
// Without this a model reads "repeated GroupCount" and has to guess what a
// GroupCount holds. A wrong guess does not fail: reading group.project when the
// field is group.name yields undefined, and the answer is quietly wrong.
//
// Shapes are shared between capabilities, so they are rendered once after all
// the declarations rather than inline with each — which also keeps the prompt
// byte-stable for caching as the match set changes.
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

// Types renders the registry's language-neutral field types into one dialect's
// notation. The registry stores protobuf's own vocabulary — "repeated Todo",
// "map<string, int>", "bytes" — because it is read by every engine; a model
// being asked for Lua should not be shown TypeScript.
type Types struct {
	// Scalar maps a neutral scalar name to this dialect's name for it. A name
	// absent from the map is passed through, which is what message and enum
	// names want: they are the same word in every dialect.
	Scalar map[string]string
	// List renders a repeated field given its already-rendered element type.
	List func(element string) string
	// Dict renders a map field given its already-rendered key and value types.
	Dict func(key, value string) string
}

// Render converts one neutral type. Three shapes exist and they never nest,
// because protobuf forbids repeated maps and map values that are themselves
// maps or repeated.
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

// Fields renders a schema's fields with a per-dialect separator and formatter,
// sorted so the same match set always produces the same bytes and the prompt
// stays cacheable.
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
