package sandbox

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mukezhz/code-mode/capability"
)

// dialect describes how programs are written for one engine. Everything a
// model needs to know that differs between engines lives here, so adding an
// engine does not touch the agent, the host, or any other engine.
type dialect struct {
	// id is the short, stable name of the language: "javascript", "starlark",
	// "lua". Callers key on this; it never changes wording.
	id string
	// prose is how the language is described to a model, which can be longer
	// and more explanatory than the id.
	prose string
	// rules are the engine-specific instructions, one per line, inserted into
	// the shared prompt frame.
	rules []string
	// example is a short, complete program in this dialect.
	example string
	// signature renders one capability as a call the model can copy.
	signature func(capability.Capability) string
	// comment wraps a description in this dialect's comment syntax.
	comment func(string) string
}

// prompt renders the shared frame around a dialect's rules and the
// declarations for matches. The frame is identical across engines so a change
// to the house rules lands everywhere at once.
func (dialect dialect) prompt(matches []capability.Match) string {
	var builder strings.Builder

	fmt.Fprintf(&builder,
		"You write short %s programs that answer a user's request by calling application capabilities.\n\nRules:\n",
		dialect.prose)
	for _, rule := range dialect.rules {
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
	builder.WriteString(dialect.declarations(matches))
	builder.WriteString("\nExample of the shape expected:\n\n")
	builder.WriteString(strings.TrimSpace(dialect.example))
	builder.WriteString("\n")

	return builder.String()
}

// declarations renders each match as a commented call signature.
func (dialect dialect) declarations(matches []capability.Match) string {
	var builder strings.Builder
	for _, match := range matches {
		builder.WriteString(dialect.comment(match.Capability.Description))
		builder.WriteString("\n")
		builder.WriteString(dialect.signature(match.Capability))
		builder.WriteString("\n\n")
	}
	return builder.String()
}

// types renders the registry's language-neutral field types into one dialect's
// notation. The registry stores protobuf's own vocabulary — "repeated Todo",
// "map<string, int>", "bytes" — because it is read by every engine; a model
// being asked for Lua should not be shown TypeScript.
type types struct {
	// scalar maps a neutral scalar name to this dialect's name for it. A name
	// absent from the map is passed through, which is what message and enum
	// names want: they are the same word in every dialect.
	scalar map[string]string
	// list renders a repeated field given its already-rendered element type.
	list func(element string) string
	// dict renders a map field given its already-rendered key and value types.
	dict func(key, value string) string
}

// render converts one neutral type. Three shapes exist and they never nest,
// because protobuf forbids repeated maps and map values that are themselves
// maps or repeated.
func (style types) render(neutral string) string {
	if element, found := strings.CutPrefix(neutral, "repeated "); found {
		return style.list(style.render(element))
	}
	if inner, found := strings.CutPrefix(neutral, "map<"); found {
		if inner, found := strings.CutSuffix(inner, ">"); found {
			// A map key is always a scalar, so the first separator splits it.
			if key, value, found := strings.Cut(inner, ", "); found {
				return style.dict(style.render(key), style.render(value))
			}
		}
	}
	if scalar, found := style.scalar[neutral]; found {
		return scalar
	}
	return neutral
}

// fields renders a schema's fields with a per-dialect separator and formatter,
// sorted so the same match set always produces the same bytes and the prompt
// stays cacheable.
func fields(schema capability.TypeSchema, style types, format func(name, fieldType string) string, empty, open, close string) string {
	if len(schema.Fields) == 0 {
		return empty
	}
	rendered := make([]string, 0, len(schema.Fields))
	for name, fieldType := range schema.Fields {
		rendered = append(rendered, format(name, style.render(fieldType)))
	}
	sort.Strings(rendered)
	return open + strings.Join(rendered, ", ") + close
}

// Declarations renders matches as TypeScript signatures. It is the JavaScript
// dialect's rendering, kept as a package function for callers that want to show
// a prompt fragment without building a runner.
//
// Prefer Runner.Prompt: it renders the dialect that will actually evaluate the
// program, rather than assuming JavaScript.
func Declarations(matches []capability.Match) string {
	return javascript.declarations(matches)
}
